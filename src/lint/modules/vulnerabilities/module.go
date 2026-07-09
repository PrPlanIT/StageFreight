package vulnerabilities

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// vulnModule is the single supply-chain vulnerability renderer. Per file it
// gathers advisory observations from both sources — the OSV-API correlation
// already attached to the file's dependencies, and a per-file osv-scanner run —
// canonicalizes them into ONE Vulnerability per advisory (deduping the former
// freshness-INFO + osv-scanner-WARN double report), and emits one lint.Finding
// per canonical vulnerability.
//
// It mirrors the freshness module's threading: when the audition provides a
// pre-resolved Snapshot it narrows that to the current file (no per-file
// resolution); standalone it self-resolves via the shared discovery.Resolver
// (the RESOLVER, not a whole-repo discovery pass) so `stagefreight lint <path>`
// scans the target, not the process's working directory.
type vulnModule struct {
	resolver *discovery.Resolver
	desired  map[string]config.ToolPinConfig
	snapshot *supplychain.Snapshot

	// osv-scanner binary, resolved once across the run. resolveErr is set only
	// when a PINNED version fails to resolve — a hard-fail of the gate.
	once       sync.Once
	binPath    string
	resolveErr error
}

func newModule() *vulnModule {
	return &vulnModule{resolver: discovery.NewResolver()}
}

func (m *vulnModule) Name() string         { return "vulnerabilities" }
func (m *vulnModule) DefaultEnabled() bool { return true }

// CacheTTL expires findings after the same window the former osv module used:
// they depend on external CVE feeds and the osv-scanner run.
func (m *vulnModule) CacheTTL() time.Duration { return 5 * time.Minute }

// AutoDetect lists the manifests and lockfiles that indicate a supply-chain
// surface — the union of what the freshness resolver and osv-scanner consume.
func (m *vulnModule) AutoDetect() []string {
	return []string{
		"go.mod",
		"Cargo.toml",
		"Cargo.lock",
		"package.json",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"requirements*.txt",
		"Pipfile",
		"Pipfile.lock",
		"poetry.lock",
		"composer.lock",
		"Gemfile.lock",
	}
}

// SetToolchainDesired implements lint.ToolchainAwareModule (osv-scanner pin). It
// also threads the pin into the resolver so the OSV-API leg's toolchain-desired
// discovery stays consistent.
func (m *vulnModule) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.desired = desired
	m.resolver.SetToolchainDesired(desired)
}

// SetSnapshot implements lint.SnapshotAwareModule.
func (m *vulnModule) SetSnapshot(snapshot *supplychain.Snapshot) { m.snapshot = snapshot }

// Configure implements lint.ConfigurableModule. The engine sources these options
// from the freshness config section, so the vulnerabilities module reads the same
// vulnerability config (min_severity, correlation enable, ignores, source
// toggles) the freshness/osv paths read — no vuln config is silently dropped.
func (m *vulnModule) Configure(opts map[string]any) error {
	return m.resolver.Configure(opts)
}

// Check gathers this file's advisory observations from both sources, reduces
// them to one vulnerability per advisory, and renders each as a finding.
func (m *vulnModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	obs, err := m.observe(ctx, file)
	if err != nil {
		return nil, err
	}
	if len(obs) == 0 {
		return nil, nil
	}
	var findings []lint.Finding
	for _, v := range analysis.Reduce(obs) {
		findings = append(findings, toFinding(v))
	}
	return findings, nil
}

// observe collects this file's advisory observations from both sources.
func (m *vulnModule) observe(ctx context.Context, file lint.FileInfo) ([]analysis.AdvisoryObservation, error) {
	var obs []analysis.AdvisoryObservation

	// (a) OSV-API leg — the vulnerabilities already correlated onto this file's
	// dependencies. Gated by the same config the freshness renderer applied to
	// its (now-removed) per-advisory findings: ignore globs, package-rule
	// disables, and per-source toggles.
	deps, err := m.deps(ctx, file)
	if err != nil {
		return nil, err
	}
	obs = append(obs, analysis.ObserveDependencies(m.eligibleDeps(deps))...)

	// (b) osv-scanner leg — a per-file scan of this lockfile, ungated (mirroring
	// the former standalone osv module). A pinned-but-unresolvable scanner
	// hard-fails the gate; unpinned+unavailable skips the scanner but keeps (a).
	base := filepath.Base(file.Path)
	if analysis.IsScannableLockfile(base, file.AbsPath) {
		bin, resErr := m.scanner(ctx, file)
		if resErr != nil {
			return nil, resErr
		}
		if bin != "" {
			scannerObs, scanErr := analysis.ObserveScanner(ctx, bin, toolchain.CleanEnv(), file.AbsPath, file.Path)
			if scanErr != nil {
				return nil, scanErr
			}
			obs = append(obs, scannerObs...)
		}
	}
	return obs, nil
}

// deps returns this file's dependencies: narrowed from the audition Snapshot
// when set (no resolution), else self-resolved via the resolver — mirroring the
// freshness module's standalone fallback.
func (m *vulnModule) deps(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	if m.snapshot != nil {
		var deps []supplychain.Dependency
		for _, dep := range m.snapshot.Dependencies {
			if dep.File == file.Path {
				deps = append(deps, dep)
			}
		}
		return deps, nil
	}
	return m.resolver.ResolveFile(ctx, file)
}

// eligibleDeps applies the config gates the freshness renderer applied to its
// per-advisory findings before they moved here: ignore globs, package-rule
// disables, and per-source toggles. The osv-scanner leg stays ungated (the
// former osv module applied none of these).
func (m *vulnModule) eligibleDeps(deps []supplychain.Dependency) []supplychain.Dependency {
	cfg := m.resolver.Config()
	var out []supplychain.Dependency
	for _, dep := range deps {
		if cfg.IsIgnored(dep.Name) || cfg.IsDisabledByRule(dep) || !cfg.SourceEnabled(dep.Ecosystem) {
			continue
		}
		out = append(out, dep)
	}
	return out
}

// scanner resolves the osv-scanner binary once. A PINNED version that fails to
// resolve returns an error (hard-fails the gate, reproducing the former osv
// module's pinned-version contract); an UNPINNED unavailable binary returns
// ("", nil) — a silent skip that still lets the OSV-API leg emit.
func (m *vulnModule) scanner(ctx context.Context, file lint.FileInfo) (string, error) {
	m.once.Do(func() {
		root := repoRoot(file)
		ver, pinned := toolchain.ResolveVersion("osv-scanner", "", m.desired)
		result, err := provision.Resolve(ctx, root, "osv-scanner", ver, "dependency vulnerability audit")
		if err != nil {
			if pinned {
				m.resolveErr = fmt.Errorf("osv-scanner pinned version %s failed to resolve: %w", ver, err)
			}
			return
		}
		m.binPath = result.Path
	})
	return m.binPath, m.resolveErr
}

// repoRoot derives the lint target root from a file's absolute and repo-relative
// paths, so tool resolution's workspace-local cache lands under the scanned tree
// — not os.Getwd(), which is wrong for `stagefreight lint <other-path>`.
func repoRoot(file lint.FileInfo) string {
	abs := filepath.ToSlash(file.AbsPath)
	rel := filepath.ToSlash(file.Path)
	if trimmed := strings.TrimSuffix(abs, rel); trimmed != abs {
		return filepath.FromSlash(strings.TrimRight(trimmed, "/"))
	}
	return filepath.Dir(file.AbsPath)
}

// toFinding renders one canonical vulnerability as a single lint finding. RuleID
// carries the advisory id (stable identity for baseline diffing); Message is
// presentation, including the affected package@version for triage (as the former
// osv `pkg@version` and freshness `name@current` messages did).
func toFinding(v analysis.Vulnerability) lint.Finding {
	summary := v.Summary
	if summary == "" {
		summary = "no description available"
	}
	msg := fmt.Sprintf("%s: %s (%s", v.ID, summary, strings.Join(v.Packages, ", "))
	if v.FixedIn != "" {
		msg += ", fixed in " + v.FixedIn
	}
	msg += ")"
	return lint.Finding{
		File:     v.File,
		Line:     v.Line,
		Module:   "vulnerabilities",
		Severity: verdictSeverity(v.Verdict),
		Message:  msg,
		RuleID:   v.ID,
	}
}

// verdictSeverity maps an analysis Verdict to a lint Severity one-to-one.
func verdictSeverity(v analysis.Verdict) lint.Severity {
	switch v {
	case analysis.VerdictCritical:
		return lint.SeverityCritical
	case analysis.VerdictWarning:
		return lint.SeverityWarning
	default:
		return lint.SeverityInfo
	}
}
