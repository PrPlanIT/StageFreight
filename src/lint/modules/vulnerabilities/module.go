package vulnerabilities

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// vulnModule is the single supply-chain vulnerability renderer. It turns a
// canonical analysis.Assessment into lint findings — exactly ONE finding per
// advisory — so a given CVE renders once regardless of how many sources
// (OSV-API correlation, osv-scanner) observed it.
//
// It builds the Assessment once per run (sync.Once) and distributes the
// resulting findings across files by each vulnerability's representative
// location, mirroring how the freshness module attributes findings to the file
// a dependency lives in. Findings are emitted the first time the module is asked
// about their file; every other Check returns the pre-computed slice for its file.
type vulnModule struct {
	once     sync.Once
	byFile   map[string][]lint.Finding
	desired  map[string]config.ToolPinConfig
	snapshot *supplychain.Snapshot
	// assessment, when set by the engine (AssessmentAwareModule), is the
	// pre-built Assessment threaded by the audition. nil → self-build.
	assessment *analysis.Assessment
}

func newModule() *vulnModule { return &vulnModule{} }

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

// SetToolchainDesired implements lint.ToolchainAwareModule (osv-scanner pin).
func (m *vulnModule) SetToolchainDesired(desired map[string]config.ToolPinConfig) {
	m.desired = desired
}

// SetSnapshot implements lint.SnapshotAwareModule.
func (m *vulnModule) SetSnapshot(snapshot *supplychain.Snapshot) { m.snapshot = snapshot }

// SetAssessment implements lint.AssessmentAwareModule.
func (m *vulnModule) SetAssessment(assessment *analysis.Assessment) { m.assessment = assessment }

// Check returns the vulnerability findings attributed to file. The Assessment is
// built exactly once (on the first Check that reaches this body); every call
// thereafter reads the pre-computed per-file map.
func (m *vulnModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	m.once.Do(func() { m.build(ctx) })
	return m.byFile[file.Path], nil
}

// build produces the Assessment and buckets its findings by representative file.
// It never fails the lint: any collection problem is logged and the module
// renders whatever the Assessment contains.
func (m *vulnModule) build(ctx context.Context) {
	m.byFile = map[string][]lint.Finding{}

	assessment := m.assessment
	if assessment == nil {
		assessment = m.selfBuild(ctx)
	}
	if assessment == nil {
		return
	}
	for _, v := range assessment.Vulnerabilities {
		f := toFinding(v)
		m.byFile[f.File] = append(m.byFile[f.File], f)
	}
}

// selfBuild produces an Assessment when the audition did not thread one
// (standalone `stagefreight lint`): it sources OSV-API observations from the
// threaded Snapshot (self-discovering one if none was threaded) and runs
// osv-scanner. Mirrors how the freshness/osv modules self-discover today.
func (m *vulnModule) selfBuild(ctx context.Context) *analysis.Assessment {
	root, _ := os.Getwd()

	snapshot := m.snapshot
	if snapshot == nil {
		s, err := discovery.Discover(ctx, nil, collectFiles(root))
		if err != nil {
			diag.Warn("vulnerabilities: dependency discovery failed: %v", err)
		}
		snapshot = s
	}

	cfg := analysis.Config{RootDir: root}
	if bin := m.resolveScanner(ctx, root); bin != "" {
		cfg.ScannerBinPath = bin
		cfg.ScannerEnv = toolchain.CleanEnv()
	}

	assessment, err := analysis.Analyze(ctx, snapshot, cfg)
	if err != nil {
		diag.Warn("vulnerabilities: %v", err)
	}
	return assessment
}

// resolveScanner resolves the osv-scanner binary. A missing (unpinned) binary is
// a silent skip; a pinned version that fails is logged but never fails the lint.
func (m *vulnModule) resolveScanner(ctx context.Context, root string) string {
	ver, pinned := toolchain.ResolveVersion("osv-scanner", "", m.desired)
	result, err := provision.Resolve(ctx, root, "osv-scanner", ver, "dependency vulnerability audit")
	if err != nil {
		if pinned {
			diag.Warn("vulnerabilities: osv-scanner pinned version %s failed to resolve: %v", ver, err)
		}
		return ""
	}
	return result.Path
}

// collectFiles walks root for regular files (skipping hidden directories),
// mirroring the lint engine's CollectFiles, so a self-discovered Snapshot sees
// the same manifest set the engine would.
func collectFiles(root string) []lint.FileInfo {
	var files []lint.FileInfo
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(rel)
			if strings.HasPrefix(base, ".") && base != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		files = append(files, lint.FileInfo{Path: rel, AbsPath: path})
		return nil
	})
	return files
}

// toFinding renders one canonical vulnerability as a single lint finding. RuleID
// carries the advisory id (stable identity for baseline diffing); Message is
// presentation, unifying the wording the freshness and osv modules used.
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
