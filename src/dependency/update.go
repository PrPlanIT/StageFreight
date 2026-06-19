package dependency

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// depStep is one rendered row in the Dependencies card. status is "ok"/"fail"
// for pass/fail steps (icon shown) or "" for an informational value row.
type depStep struct {
	label  string
	detail string
	status string
	dur    time.Duration
}

// renderDepsCard renders the collected dependency-update steps as a single
// output.Section — the same card idiom the rest of the pipeline uses — instead
// of the raw "[deps:diag]" stderr stream. Rendered once via defer so it covers
// every return path (success, early skip, error) and never interleaves with the
// live toolchain panel that prints mid-run.
func renderDepsCard(w io.Writer, steps []depStep, total time.Duration) {
	if len(steps) == 0 {
		return
	}
	color := output.UseColor()
	sec := output.NewSection(w, "Dependencies", total, color)
	for _, s := range steps {
		dur := ""
		if s.dur > 0 {
			dur = "  (" + s.dur.Round(time.Millisecond).String() + ")"
		}
		switch s.status {
		case "ok":
			sec.Row("%-16s%s  %s%s", s.label, output.StatusIcon("success", color), s.detail, dur)
		case "fail":
			sec.Row("%-16s%s  %s%s", s.label, output.StatusIcon("failed", color), s.detail, dur)
		default:
			sec.Row("%-16s%s%s", s.label, s.detail, dur)
		}
	}
	sec.Close()
}

// UpdateResult holds the outcome of a dependency update run.
type UpdateResult struct {
	Applied           []AppliedUpdate
	Skipped           []SkippedDep
	Toolchains        []ToolchainDependency // resolved build toolchains for SBOM/reporting
	Verified          bool
	VerifyLog         string
	VerifyErr         error
	Artifacts         []string
	ArtifactErr       error    // non-nil if artifact generation failed (non-fatal)
	TouchedModuleDirs []string // repoRoot-relative Go module dirs that were updated
	FilesChanged      []string // files modified by updates (go.mod, go.sum, Dockerfiles)
}

// AppliedUpdate records a single dependency that was successfully updated.
type AppliedUpdate struct {
	Dep        freshness.Dependency
	OldVer     string
	NewVer     string
	UpdateType string // "major", "minor", "patch", "tag"
	CVEsFixed  []string
}

// Update resolves, filters, applies, verifies, and generates artifacts for dependency updates.
func Update(ctx context.Context, cfg UpdateConfig, deps []freshness.Dependency) (*UpdateResult, error) {
	result := &UpdateResult{}

	w := cfg.Writer
	if w == nil {
		w = os.Stderr
	}
	var steps []depStep
	overall := time.Now()
	defer func() { renderDepsCard(w, steps, time.Since(overall)) }()

	// 1. Discover repo root
	repoRoot, err := discoverRepoRoot(cfg.RootDir)
	if err != nil {
		return result, fmt.Errorf("not a git repository: %w", err)
	}

	// 2. Check tracked files are clean
	t0 := time.Now()
	if err := checkGitClean(ctx, repoRoot); err != nil {
		steps = append(steps, depStep{label: "git clean", status: "fail", detail: err.Error()})
		return result, err
	}
	steps = append(steps, depStep{label: "git clean", status: "ok", dur: time.Since(t0)})

	// 3. Detect git-tracked files
	t0 = time.Now()
	trackedFiles, err := gitTrackedFiles(ctx, repoRoot)
	if err != nil {
		steps = append(steps, depStep{label: "tracked files", status: "fail", detail: err.Error()})
		return result, fmt.Errorf("listing tracked files: %w", err)
	}
	steps = append(steps, depStep{label: "tracked files", detail: fmt.Sprintf("%d", len(trackedFiles)), dur: time.Since(t0)})

	// 4. Filter update candidates
	candidates, skipped := FilterUpdateCandidates(deps, cfg, trackedFiles)
	result.Skipped = skipped
	steps = append(steps, depStep{label: "candidates", detail: fmt.Sprintf("%d go-mod (from %d deps)", len(candidates), len(deps))})

	if len(candidates) == 0 {
		steps = append(steps, depStep{label: "apply", detail: "no candidates — skipped"})
		return result, nil
	}

	// 5. Group by ecosystem and apply
	gomodDeps, dockerDeps, toolchainDeps, cargoDeps := groupByEcosystem(candidates)
	steps = append(steps, depStep{label: "apply", detail: fmt.Sprintf("%d gomod · %d docker · %d toolchain · %d cargo", len(gomodDeps), len(dockerDeps), len(toolchainDeps), len(cargoDeps))})

	if len(gomodDeps) > 0 {
		t0 = time.Now()
		applied, goSkipped, touchedDirs, touchedFiles, err := applyGoUpdates(ctx, gomodDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "go updates", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying Go updates: %w", err)
		}
		steps = append(steps, depStep{label: "go updates", status: "ok", detail: fmt.Sprintf("touched: %s", strings.Join(touchedDirs, ", ")), dur: time.Since(t0)})
		result.Applied = append(result.Applied, applied...)
		result.Skipped = append(result.Skipped, goSkipped...)
		result.TouchedModuleDirs = touchedDirs
		result.FilesChanged = append(result.FilesChanged, touchedFiles...)
	}

	if len(dockerDeps) > 0 {
		t0 = time.Now()
		applied, dkSkipped, touchedFiles, err := applyDockerfileUpdates(dockerDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "dockerfile", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying Dockerfile updates: %w", err)
		}
		steps = append(steps, depStep{label: "dockerfile", status: "ok", dur: time.Since(t0)})
		result.Applied = append(result.Applied, applied...)
		result.Skipped = append(result.Skipped, dkSkipped...)
		result.FilesChanged = append(result.FilesChanged, touchedFiles...)
	}

	if len(toolchainDeps) > 0 {
		t0 = time.Now()
		applied, tcSkipped, touchedFiles, err := applyToolchainDesiredUpdates(toolchainDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "toolchain", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying toolchain updates: %w", err)
		}
		steps = append(steps, depStep{label: "toolchain", status: "ok", dur: time.Since(t0)})
		result.Applied = append(result.Applied, applied...)
		result.Skipped = append(result.Skipped, tcSkipped...)
		result.FilesChanged = append(result.FilesChanged, touchedFiles...)
	}

	if len(cargoDeps) > 0 {
		t0 = time.Now()
		applied, cgSkipped, touchedFiles, err := applyCargoUpdates(ctx, cargoDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "cargo", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying Cargo updates: %w", err)
		}
		steps = append(steps, depStep{label: "cargo", status: "ok", dur: time.Since(t0)})
		result.Applied = append(result.Applied, applied...)
		result.Skipped = append(result.Skipped, cgSkipped...)
		result.FilesChanged = append(result.FilesChanged, touchedFiles...)
	}

	// 5a. Normalize, deduplicate, and sort FilesChanged
	result.FilesChanged = deduplicateAndSort(result.FilesChanged)

	// 5b. Sync go directives to match golang builder versions.
	// Two sources of sync targets, merged and deduped:
	//   - Applied builder updates (Dockerfile golang image was bumped this run)
	//   - Existing drift (Dockerfile already up-to-date but go.mod lags behind)
	var syncResolved goDirectiveSyncResult
	if hasAppliedGolangBuilderUpdate(result.Applied) {
		syncResolved = collectGoDirectiveSyncTargets(repoRoot, result.Applied)
	}
	driftResolved := detectGoDirectiveDrift(repoRoot, deps)
	syncResolved = mergeGoDirectiveSyncResults(syncResolved, driftResolved)

	if len(syncResolved.Targets) > 0 || len(syncResolved.Conflicted) > 0 {
		t0 = time.Now()
		if err := syncGoDirectivesFromResolved(ctx, repoRoot, result, syncResolved); err != nil {
			steps = append(steps, depStep{label: "sync directives", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("syncing go directives: %w", err)
		}
		steps = append(steps, depStep{label: "sync directives", status: "ok", detail: fmt.Sprintf("%d target · %d conflicted", len(syncResolved.Targets), len(syncResolved.Conflicted)), dur: time.Since(t0)})
		result.Toolchains = collectToolchainDepsFromResolved(syncResolved, result.Applied)
	}

	// 6. Verify — only run on Go module dirs that were actually updated
	if cfg.Verify && len(result.TouchedModuleDirs) > 0 {
		absDirs := make([]string, 0, len(result.TouchedModuleDirs))
		for _, d := range result.TouchedModuleDirs {
			absDirs = append(absDirs, filepath.Join(repoRoot, d))
		}
		t0 = time.Now()
		log, verifyErr := Verify(ctx, absDirs, repoRoot, true, cfg.Vulncheck)
		status, detail := "ok", "go test ./... + govulncheck"
		if verifyErr != nil {
			status, detail = "fail", "go test ./... + govulncheck — "+verifyErr.Error()
		}
		steps = append(steps, depStep{label: "verify", status: status, detail: detail, dur: time.Since(t0)})
		result.Verified = true
		result.VerifyLog = log
		result.VerifyErr = verifyErr
	}

	// 7. Generate artifacts
	outputDir := cfg.OutputDir
	if outputDir == "" {
		outputDir = ".stagefreight/deps"
	}
	if !filepath.IsAbs(outputDir) {
		outputDir = filepath.Join(repoRoot, outputDir)
	}

	artifacts, artErr := GenerateArtifacts(ctx, repoRoot, outputDir, result, cfg.Bundle)
	result.Artifacts = artifacts
	result.ArtifactErr = artErr

	return result, nil
}

func groupByEcosystem(deps []freshness.Dependency) (gomod, docker, tc, cargo []freshness.Dependency) {
	for _, dep := range deps {
		switch dep.Ecosystem {
		case freshness.EcosystemGoMod:
			gomod = append(gomod, dep)
		case freshness.EcosystemDockerImage, freshness.EcosystemGitHubRelease:
			docker = append(docker, dep)
		case freshness.EcosystemToolchain:
			tc = append(tc, dep)
		case freshness.EcosystemCargo:
			cargo = append(cargo, dep)
		}
	}
	return
}

// discoverRepoRoot finds the git repository root from the given directory.
func discoverRepoRoot(dir string) (string, error) {
	repo, err := gitstate.OpenRepo(dir)
	if err != nil {
		return "", err
	}
	return gitstate.RepoRoot(repo)
}

// checkGitClean verifies that tracked files have no uncommitted changes.
func checkGitClean(_ context.Context, repoRoot string) error {
	repo, err := gitstate.OpenRepo(repoRoot)
	if err != nil {
		return fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("opening worktree: %w", err)
	}
	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("reading worktree status: %w", err)
	}

	var unstaged, staged []string
	for path, s := range status {
		if s.Worktree != git.Unmodified {
			unstaged = append(unstaged, "  "+path)
		}
		if s.Staging != git.Unmodified {
			staged = append(staged, "  "+path)
		}
	}
	sort.Strings(unstaged)
	sort.Strings(staged)

	if len(unstaged) > 0 {
		return fmt.Errorf("tracked files have unstaged changes:\n%s", strings.Join(unstaged, "\n"))
	}
	if len(staged) > 0 {
		return fmt.Errorf("tracked files have staged changes:\n%s", strings.Join(staged, "\n"))
	}
	return nil
}

// deduplicateAndSort normalizes, deduplicates, and sorts a string slice.
func deduplicateAndSort(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		normalized := filepath.Clean(p)
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	sort.Strings(out)
	return out
}

// gitTrackedFiles returns a set of repo-root-relative paths for all tracked files.
func gitTrackedFiles(_ context.Context, repoRoot string) (map[string]bool, error) {
	repo, err := gitstate.OpenRepo(repoRoot)
	if err != nil {
		return nil, err
	}
	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("resolving HEAD: %w", err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, fmt.Errorf("loading HEAD commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("loading HEAD tree: %w", err)
	}

	tracked := make(map[string]bool)
	err = tree.Files().ForEach(func(f *object.File) error {
		tracked[f.Name] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("iterating tree: %w", err)
	}
	return tracked, nil
}
