package vulnerabilities

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
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
	desired  map[string]config.ToolConstraint
	snapshot *supplychain.Snapshot

	// osv-scanner binary, resolved once across the run. resolveErr is set only
	// when a PINNED version fails to resolve — a hard-fail of the gate.
	once       sync.Once
	binPath    string
	resolveErr error

	// govulncheck + its Go toolchain, resolved once across the run for
	// reachability enrichment. gvErr is NEVER hard-failed: reachability is
	// enrichment, so a provisioning failure just means no downgrade (fail-closed).
	gvOnce sync.Once
	gvBin  string
	gvEnv  []string
	gvErr  error
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
func (m *vulnModule) SetToolchainDesired(desired map[string]config.ToolConstraint) {
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

// Check is the mis-dispatch guard for this whole-repo module. The engine routes
// whole-repo modules to CheckAll and never calls Check; a call here means
// something bypassed that dispatch, so fail loud rather than silently reduce one
// file in isolation (which would resurrect the cross-file double-report this
// module exists to prevent).
func (m *vulnModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	return nil, fmt.Errorf("vulnerabilities is a whole-repo module; the engine must call CheckAll, not Check")
}

// CheckAll implements lint.WholeRepoModule. It gathers advisory observations
// from BOTH sources across EVERY file, then reduces the whole set at once — so
// an advisory observed on a manifest (OSV-API leg, e.g. package.json) and on its
// separate lockfile (osv-scanner leg, e.g. package-lock.json) canonicalizes into
// ONE vulnerability. A per-file reduce could only ever see one leg of such an
// advisory, double-reporting it; accumulating first is what makes cross-file
// dedup work for ecosystems whose manifest and lockfile are distinct files.
func (m *vulnModule) CheckAll(ctx context.Context, files []lint.FileInfo) ([]lint.Finding, error) {
	var allObs []analysis.AdvisoryObservation
	var errs []error
	for _, file := range files {
		obs, err := m.observe(ctx, file)
		if err != nil {
			// Isolate per-file failures: one file that fails to observe (a
			// malformed manifest, an osv-scanner error, a pinned-scanner
			// resolution failure) must NOT discard the observations already
			// collected from other files. The old per-file engine dispatch ran
			// each file in its own goroutine, so a single file's error only
			// dropped that file — every other file's findings still gated. Match
			// that: record the error, keep going, and still reduce+render the
			// observations that succeeded. Returning nil here instead would let a
			// single bad file suppress a real critical on go.mod and — because the
			// gate keys on the findings slice, not the returned error — ship it.
			errs = append(errs, fmt.Errorf("%s: %w", file.Path, err))
			continue
		}
		allObs = append(allObs, obs...)
	}

	// Enrich with reachability (govulncheck) when there is a Go advisory to
	// potentially downgrade — Assess folds a proven-unreachable vuln to Info.
	// reg is nil on non-Go repos or when govulncheck can't provision, in which
	// case Assess reduces to severity-only (identical to the former Reduce path).
	reg, target := m.reachability(ctx, files, allObs)
	vulns := analysis.Assess(ctx, allObs, target, reg)

	// Persist the canonical source Assessment as a cross-phase catalogue artifact
	// so review can reconcile its image-scan findings against these source
	// findings by vuln ID. Best-effort and additive — never affects the lint.
	m.persistCatalogue(files, vulns)

	var findings []lint.Finding
	for _, v := range vulns {
		findings = append(findings, toFinding(v))
	}
	if len(errs) > 0 {
		return findings, errors.Join(errs...)
	}
	return findings, nil
}

// persistCatalogue writes the canonical source Assessment to
// .stagefreight/security/source-vulns.json under the scanned repo root — the
// cross-phase catalogue the review phase reads back to collapse source and image
// vulnerabilities by advisory ID. Strictly additive and best-effort: any failure
// is swallowed so it can never change the lint result, and nothing is written
// when there is no vulnerability to record.
func (m *vulnModule) persistCatalogue(files []lint.FileInfo, vulns []analysis.Vulnerability) {
	// The catalogue is a cross-phase CI artifact — the audition writes it, review
	// reads it. Produce it only in CI (same check as output.IsCI, inlined to avoid
	// pulling the output package into a lint module): that scopes it to the phase
	// pipeline and, since the CI audition scans the whole repo while a local `lint
	// --level changed` sees only changed files, prevents a partial local run from
	// overwriting a fuller catalogue.
	if os.Getenv("CI") != "true" {
		return
	}
	if len(files) == 0 || len(vulns) == 0 {
		return
	}
	data, err := analysis.MarshalSourceAssessment(vulns)
	if err != nil {
		return
	}
	dir := filepath.Join(repoRoot(files[0]), ".stagefreight", "security")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "source-vulns.json"), data, 0o644)
}

// reachability builds the Go reachability contributor when (a) there is at least
// one Go advisory a downgrade could apply to and (b) govulncheck plus a Go
// toolchain to drive it provision. Returns (nil, zero Target) to SKIP enrichment
// — Assess then reduces to severity-only. Fail-closed: any provisioning failure
// means no reachability, never a wrong downgrade or a crash. Skipping the
// compile-heavy govulncheck run when there are no Go advisories keeps lint cheap
// on non-Go repos and on Go repos with no known vulnerabilities.
func (m *vulnModule) reachability(ctx context.Context, files []lint.FileInfo, obs []analysis.AdvisoryObservation) (*evidence.Registry, evidence.Target) {
	if len(files) == 0 {
		return nil, evidence.Target{}
	}
	hasGo := false
	for _, o := range obs {
		if o.Ecosystem == supplychain.EcosystemGoMod {
			hasGo = true
			break
		}
	}
	if !hasGo {
		return nil, evidence.Target{}
	}
	root := repoRoot(files[0])
	bin, env, err := m.govulncheck(ctx, root)
	if err != nil || bin == "" {
		return nil, evidence.Target{}
	}
	reg := evidence.NewRegistry(&evidence.GoReachability{Run: govulncheckRunner(bin, env)})
	return reg, evidence.Target{EcosystemDir: map[string]string{"go": root}}
}

// govulncheck provisions the govulncheck binary AND a Go toolchain to drive it
// (govulncheck shells out to `go` for call-graph analysis), resolved once across
// the run. It returns the binary path and the environment to run it with (Go on
// PATH + the shared module/build caches). Unlike the PINNED osv-scanner, a
// govulncheck failure is never hard — reachability is enrichment.
func (m *vulnModule) govulncheck(ctx context.Context, root string) (string, []string, error) {
	m.gvOnce.Do(func() {
		gvVer, _ := toolchain.ResolveVersion(root, "govulncheck", "", m.desired)
		gvRes, err := provision.Resolve(ctx, root, "govulncheck", gvVer, "vulnerability reachability analysis")
		if err != nil {
			m.gvErr = err
			return
		}
		goRes, err := provision.Resolve(ctx, root, "go", toolchain.ResolveGoVersion(root, root), "Go toolchain for reachability analysis")
		if err != nil {
			m.gvErr = err
			return
		}
		m.gvBin = gvRes.Path
		m.gvEnv = govulncheckEnv(goRes.Path)
	})
	return m.gvBin, m.gvEnv, m.gvErr
}

// govulncheckEnv mirrors the test suite's go env (goSuiteEnv): a clean base, the
// shared module/build caches, and the provisioned go's OWN directory FIRST on
// PATH — load-bearing, since govulncheck spawns a child `go` for the compile and
// would otherwise fail with `exec: "go": executable file not found`.
func govulncheckEnv(goBin string) []string {
	env := toolchain.CleanEnv()
	if gomod, gocache := toolchain.GoCacheDirs(); gomod != "" {
		env = append(env, "GOMODCACHE="+gomod, "GOCACHE="+gocache)
	}
	return setEnvVar(env, "PATH", filepath.Dir(goBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// govulncheckRunner returns the GoReachability.Run closure: it runs the
// provisioned govulncheck over the whole module rooted at dir in -json mode.
// govulncheck exits non-zero when it finds vulnerabilities but still emits valid
// JSON, so a non-empty output is success regardless of exit code.
func govulncheckRunner(bin string, env []string) func(ctx context.Context, dir string) ([]byte, error) {
	return func(ctx context.Context, dir string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, "-json", "./...")
		if dir != "" {
			cmd.Dir = dir
		}
		cmd.Env = env
		out, _ := cmd.Output()
		if len(out) == 0 {
			return nil, fmt.Errorf("govulncheck produced no output")
		}
		return out, nil
	}
}

// setEnvVar sets key=val in a KEY=VALUE slice, replacing any existing entry so a
// base env's PATH is overridden rather than shadowed.
func setEnvVar(env []string, key, val string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + val
			return env
		}
	}
	return append(env, prefix+val)
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
		ver, pinned := toolchain.ResolveVersion(root, "osv-scanner", "", m.desired)
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
	// Disclose the reachability verdict that drove the severity — a downgrade
	// must show its reason.
	if r, ok := reachabilityOf(v); ok {
		msg += reachabilityNote(r)
	}
	return lint.Finding{
		File:     v.File,
		Line:     v.Line,
		Module:   "vulnerabilities",
		Severity: verdictSeverity(v.Verdict),
		Message:  msg,
		RuleID:   v.ID,
	}
}

// reachabilityOf extracts the reachability evidence attached to a canonical
// vulnerability, if a contributor produced it.
func reachabilityOf(v analysis.Vulnerability) (evidence.ReachabilityEvidence, bool) {
	for _, e := range v.Evidence {
		if r, ok := e.(evidence.ReachabilityEvidence); ok {
			return r, true
		}
	}
	return evidence.ReachabilityEvidence{}, false
}

// reachabilityNote discloses the reachability verdict that drove the severity.
// Unknown (no analyzer ran) is not annotated — only a proven reachable or
// unreachable state, since only those change the outcome.
func reachabilityNote(r evidence.ReachabilityEvidence) string {
	switch r.State {
	case evidence.ReachUnreachable:
		if len(r.Facts) > 0 {
			return fmt.Sprintf(" [unreachable: %s]", r.Facts[0])
		}
		return " [unreachable]"
	case evidence.ReachReachable:
		if len(r.Facts) > 0 {
			return fmt.Sprintf(" [reachable: %s]", r.Facts[0])
		}
		return " [reachable]"
	default:
		return ""
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
