package freshness

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

// freshnessModule implements lint.Module and lint.ConfigurableModule.
type freshnessModule struct {
	cfg     FreshnessConfig
	http    *httpClient
	desired map[string]config.ToolPinConfig
}

func (m *freshnessModule) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.desired = desired
}

func newModule() *freshnessModule {
	return &freshnessModule{cfg: DefaultConfig()}
}

func (m *freshnessModule) Name() string        { return "freshness" }
func (m *freshnessModule) DefaultEnabled() bool { return true }

// CacheTTL implements lint.CacheTTLModule. Freshness findings depend on
// external registries and CVE feeds, so they expire after the configured TTL.
func (m *freshnessModule) CacheTTL() time.Duration { return m.cfg.cacheTTL() }

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
	cfg, err := parseConfig(opts)
	if err != nil {
		return err
	}
	m.cfg = cfg
	m.http = newHTTPClient(cfg.Timeout)
	return nil
}

// Check dispatches to the appropriate checker based on filename.
// Each checker extracts []Dependency, resolves latest versions, and
// converts to lint findings. The raw dependencies are preserved for
// future update commands.
func (m *freshnessModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	// Lazy-init HTTP client if Configure was not called (defaults).
	if m.http == nil {
		m.http = newHTTPClient(m.cfg.Timeout)
	}

	deps, err := m.resolveFile(ctx, file)
	if err != nil {
		return nil, err
	}

	m.correlateVulns(ctx, deps)
	return m.depsToFindings(deps), nil
}

// resolveFile dispatches to the appropriate checker based on filename
// and returns raw Dependency structs (no lint-finding conversion).
func (m *freshnessModule) resolveFile(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	base := filepath.Base(file.Path)
	switch {
	case isDockerfile(base):
		return m.checkDockerfile(ctx, file)
	case base == "go.mod":
		return m.checkGoMod(ctx, file)
	case base == ".stagefreight.yml":
		// Toolchain desired versions — config-driven discovery
		return m.checkToolchainDesired(ctx, m.desired), nil
	case base == "Cargo.toml":
		return m.checkCargo(ctx, file)
	case base == "package.json":
		return m.checkNpm(ctx, file)
	case base == "requirements.txt" || strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return m.checkPip(ctx, file)
	case base == "Pipfile":
		return m.checkPip(ctx, file)
	default:
		return nil, nil
	}
}

// depsToFindings converts resolved dependencies into lint findings,
// applying package rules, severity mapping, tolerance, vulnerability
// escalation, and ignore rules.
func (m *freshnessModule) depsToFindings(deps []Dependency) []lint.Finding {
	var findings []lint.Finding

	for _, dep := range deps {
		if m.cfg.isIgnored(dep.Name) {
			continue
		}
		if m.cfg.isDisabledByRule(dep) {
			continue
		}
		if !m.cfg.sourceEnabled(dep.Ecosystem) {
			continue
		}

		// Emit vulnerability findings regardless of version freshness.
		// A dep can be on the latest version and still have unpatched CVEs.
		findings = append(findings, m.vulnFindings(dep)...)

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

		if dep.Latest == "" || dep.Current == dep.Latest {
			continue
		}

		delta := compareDependencyVersions(dep.Current, dep.Latest, dep.Ecosystem)
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
		updateType := dominantUpdateType(delta)

		// Resolve effective severity config (global → package rule override).
		sevCfg := m.cfg.effectiveSeverity(dep, updateType)

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
		if len(dep.Vulnerabilities) > 0 && m.cfg.vulnSeverityOverride() {
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

		// Annotate group if a package rule assigns one.
		if group := m.cfg.groupForDep(dep, updateType); group != "" {
			finding.Message += fmt.Sprintf(" [group: %s]", group)
		}

		findings = append(findings, finding)
	}

	return findings
}

// vulnFindings produces individual findings for each known vulnerability.
func (m *freshnessModule) vulnFindings(dep Dependency) []lint.Finding {
	if len(dep.Vulnerabilities) == 0 {
		return nil
	}

	var findings []lint.Finding
	for _, v := range dep.Vulnerabilities {
		sev := vulnSeverityToLint(v.Severity)
		msg := fmt.Sprintf("%s@%s has known vulnerability %s: %s", dep.Name, dep.Current, v.ID, v.Summary)
		if v.FixedIn != "" {
			msg += fmt.Sprintf(" (fixed in %s)", v.FixedIn)
		}
		findings = append(findings, lint.Finding{
			File:     dep.File,
			Line:     dep.Line,
			Module:   "freshness",
			Severity: sev,
			Message:  msg,
		})
	}
	return findings
}

// vulnSeverityToLint maps OSV severity labels to lint severity.
func vulnSeverityToLint(sev string) lint.Severity {
	switch strings.ToUpper(sev) {
	case "CRITICAL", "HIGH":
		return lint.SeverityCritical
	case "MODERATE":
		return lint.SeverityWarning
	default:
		return lint.SeverityInfo
	}
}

// dominantUpdateType returns "major", "minor", or "patch" for the
// highest-priority axis in a delta.
func dominantUpdateType(d VersionDelta) string {
	if d.Major > 0 {
		return "major"
	}
	if d.Minor > 0 {
		return "minor"
	}
	return "patch"
}

// isDockerfile returns true for Dockerfile, Dockerfile.*, and *.dockerfile.
func isDockerfile(base string) bool {
	if base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
		return true
	}
	return strings.HasSuffix(base, ".dockerfile")
}
