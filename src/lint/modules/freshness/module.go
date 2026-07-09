package freshness

import (
	"context"
	"fmt"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// freshnessModule implements lint.Module and lint.ConfigurableModule. It is a
// thin renderer: all resolution (registry lookups, vulnerability correlation,
// config) lives in discovery.Resolver; this module converts resolved
// dependencies into lint findings.
type freshnessModule struct {
	resolver *discovery.Resolver

	// snapshot, when set by the engine (SnapshotAwareModule), is a
	// pre-resolved supplychain.Snapshot shared across the audition — Check
	// narrows it to the current file instead of resolving on demand. nil
	// when no audition-provided Snapshot exists (e.g. standalone
	// `stagefreight lint`), in which case Check falls back to
	// resolver.ResolveFile as before.
	snapshot *supplychain.Snapshot
}

func (m *freshnessModule) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.resolver.SetToolchainDesired(desired)
}

// SetSnapshot implements lint.SnapshotAwareModule.
func (m *freshnessModule) SetSnapshot(snapshot *supplychain.Snapshot) {
	m.snapshot = snapshot
}

func newModule() *freshnessModule {
	return &freshnessModule{resolver: discovery.NewResolver()}
}

func (m *freshnessModule) Name() string         { return "freshness" }
func (m *freshnessModule) DefaultEnabled() bool { return true }

// CacheTTL implements lint.CacheTTLModule. Freshness findings depend on
// external registries and CVE feeds, so they expire after the configured TTL.
func (m *freshnessModule) CacheTTL() time.Duration { return m.resolver.Config().CacheTTL() }

func (m *freshnessModule) AutoDetect() []string {
	return []string{
		"Dockerfile*",
		"*.dockerfile",
		".stagefreight.yml",
		"go.mod",
		"Cargo.toml",
		"package.json",
		"requirements*.txt",
		"Pipfile",
	}
}

// Configure implements lint.ConfigurableModule.
func (m *freshnessModule) Configure(opts map[string]any) error {
	return m.resolver.Configure(opts)
}

// Check renders findings for file, then converts the resolved dependencies
// into lint findings. When an audition-provided Snapshot is set, it narrows
// the pre-resolved dependency set to this file — NO resolution happens here.
// Otherwise it falls back to resolving this file directly via the resolver,
// which is what keeps standalone `stagefreight lint` working. Either way,
// depsToFindings does the rendering (rules, severity, vulnerabilities).
func (m *freshnessModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	if m.snapshot != nil {
		var deps []supplychain.Dependency
		for _, dep := range m.snapshot.Dependencies {
			if dep.File == file.Path {
				deps = append(deps, dep)
			}
		}
		return m.depsToFindings(deps), nil
	}

	deps, err := m.resolver.ResolveFile(ctx, file)
	if err != nil {
		return nil, err
	}
	return m.depsToFindings(deps), nil
}

// depsToFindings converts resolved dependencies into lint findings,
// applying package rules, severity mapping, tolerance, vulnerability
// escalation, and ignore rules.
func (m *freshnessModule) depsToFindings(deps []supplychain.Dependency) []lint.Finding {
	var findings []lint.Finding
	cfg := m.resolver.Config()

	for _, dep := range deps {
		if cfg.IsIgnored(dep.Name) {
			continue
		}
		if cfg.IsDisabledByRule(dep) {
			continue
		}
		if !cfg.SourceEnabled(dep.Ecosystem) {
			continue
		}

		// Per-advisory vulnerability findings are rendered by the dedicated
		// vulnerabilities module (one finding per advisory across all sources),
		// not here. Freshness still annotates the outdated-dependency finding
		// with the dep's CVE count / severity escalation below, reading the
		// same discovery-correlated dep.Vulnerabilities.

		// Emit advisory finding for non-versioned/pre-release tags with
		// stable releases available (e.g. sha-pinned images).
		if dep.Advisory != "" {
			findings = append(findings, lint.Finding{
				File:     dep.File,
				Line:     dep.Line,
				Module:   "freshness",
				Severity: lint.SeverityInfo,
				Message:  dep.Advisory,
			})
		}

		// Unresolved: the lookup failed — surface degraded freshness as a warning
		// rather than silently passing it off as current. Unknown ≠ outdated, but
		// unknown must never render as healthy.
		if dep.ResolutionError != "" {
			findings = append(findings, lint.Finding{
				File:     dep.File,
				Line:     dep.Line,
				Module:   "freshness",
				Severity: lint.SeverityWarning,
				Message:  fmt.Sprintf("%s: freshness unresolved — %s", dep.Name, dep.ResolutionError),
			})
			continue
		}

		if dep.Latest == "" || dep.Current == dep.Latest {
			continue
		}

		delta := version.CompareDependencyVersions(dep.Current, dep.Latest, dep.Ecosystem)
		if delta.IsZero() {
			// Versions parsed equal — might be non-semver difference.
			if dep.Current != dep.Latest {
				findings = append(findings, lint.Finding{
					File:     dep.File,
					Line:     dep.Line,
					Module:   "freshness",
					Severity: lint.SeverityInfo,
					Message:  fmt.Sprintf("%s %s → %s available", dep.Name, dep.Current, dep.Latest),
				})
			}
			continue
		}

		// Determine the dominant update type for rule matching.
		updateType := version.DominantUpdateType(delta)

		// Resolve effective severity config (global → package rule override).
		sevCfg := cfg.EffectiveSeverity(dep, updateType)

		sev, msg, ok := mapSeverity(delta, sevCfg)
		if !ok && len(dep.Vulnerabilities) == 0 {
			continue // within tolerance and no CVEs
		}
		if !ok {
			// Within version tolerance but has CVEs — still report.
			sev = lint.SeverityInfo
			msg = "within tolerance"
		}

		// Escalate severity if dep has known vulnerabilities and override is on.
		if len(dep.Vulnerabilities) > 0 && cfg.VulnSeverityOverride() {
			sev = lint.SeverityCritical
		}

		finding := lint.Finding{
			File:     dep.File,
			Line:     dep.Line,
			Module:   "freshness",
			Severity: sev,
			Message:  fmt.Sprintf("%s %s → %s available (%s)", dep.Name, dep.Current, dep.Latest, msg),
		}

		// Annotate CVE count if present.
		if n := len(dep.Vulnerabilities); n > 0 {
			finding.Message += fmt.Sprintf(" [%d CVE(s)]", n)
		}

		// Disclose a newer release withheld by the supply-chain cooldown.
		if dep.CooldownHeld != "" {
			finding.Message += fmt.Sprintf(" [%s held: <%s cooldown]", dep.CooldownHeld, cfg.MinReleaseAge)
		}

		// Annotate group if a package rule assigns one.
		if group := cfg.GroupForDep(dep, updateType); group != "" {
			finding.Message += fmt.Sprintf(" [group: %s]", group)
		}

		findings = append(findings, finding)
	}

	return findings
}
