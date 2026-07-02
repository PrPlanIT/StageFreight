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
	"github.com/PrPlanIT/StageFreight/src/workspace"
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
		case "skip":
			sec.Row("%-16s%s  %s%s", s.label, output.Dimmed("↷", color), s.detail, dur)
		default:
			sec.Row("%-16s%s%s", s.label, s.detail, dur)
		}
	}
	sec.Close()
}

// eligibleDetail renders the candidate count and the ecosystems that actually have
// candidates — never fixed zero slots. "none" when there are no candidates.
func eligibleDetail(total int, byEcosystem map[string]int) string {
	if total == 0 {
		return "none"
	}
	var ecos []string
	for _, k := range []string{"gomod", "docker", "toolchain", "cargo"} {
		if byEcosystem[k] > 0 {
			ecos = append(ecos, k)
		}
	}
	noun := "candidate"
	if total != 1 {
		noun = "candidates"
	}
	return fmt.Sprintf("%d %s (%s)", total, noun, strings.Join(ecos, ", "))
}

// ecosystemStep renders one ecosystem's outcome as an event row — updated or
// skipped. Ecosystems are attributes of activity: this row exists ONLY because the
// ecosystem had a candidate, so the card never accumulates zero rows as ecosystems
// are added.
func ecosystemStep(eco string, applied []AppliedUpdate, skipped []SkippedDep, touched []string, dur time.Duration) depStep {
	switch {
	case len(applied) > 0:
		detail := fmt.Sprintf("updated %d", len(applied))
		if len(touched) > 0 {
			detail += " · " + strings.Join(touched, ", ")
		}
		return depStep{label: eco, status: "ok", detail: detail, dur: dur}
	case len(skipped) > 0:
		return depStep{label: eco, status: "skip", detail: "skipped — " + skipSummary(skipped), dur: dur}
	default:
		return depStep{label: eco, status: "ok", detail: "no changes", dur: dur}
	}
}

// cargoStep renders the cargo ecosystem outcome like ecosystemStep, but augments the applied
// detail with lock-churn amplification — intended packages vs lockfile entries actually
// mutated. A targeted lock-sync should keep this near 1×; a high ratio (flagged ⚠) means the
// resolve moved many transitives and the reviewer should look at the lockfile diff scope.
func cargoStep(applied []AppliedUpdate, skipped []SkippedDep, churn CargoChurn, dur time.Duration) depStep {
	if len(applied) == 0 {
		return ecosystemStep("cargo", applied, skipped, nil, dur)
	}
	detail := fmt.Sprintf("updated %d", len(applied))
	if churn.Intended > 0 {
		detail += fmt.Sprintf(" · lock %d→%d", churn.Intended, churn.Mutated)
		if churn.Mutated >= churn.Intended*5 {
			detail += fmt.Sprintf(" (%d× ⚠)", churn.Mutated/churn.Intended)
		}
	}
	return depStep{label: "cargo", status: "ok", detail: detail, dur: dur}
}

// skipSummary collapses skip reasons to one phrase when uniform, else a count.
func skipSummary(skipped []SkippedDep) string {
	if len(skipped) == 0 {
		return ""
	}
	first := skipped[0].Reason
	for _, s := range skipped[1:] {
		if s.Reason != first {
			return fmt.Sprintf("%d skipped", len(skipped))
		}
	}
	return first
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

	// 3. Scan scope: deps discovered · files tracked.
	t0 = time.Now()
	trackedFiles, err := gitTrackedFiles(ctx, repoRoot)
	if err != nil {
		steps = append(steps, depStep{label: "scan", status: "fail", detail: err.Error()})
		return result, fmt.Errorf("listing tracked files: %w", err)
	}
	steps = append(steps, depStep{label: "scan", detail: fmt.Sprintf("%d deps · %d files", len(deps), len(trackedFiles)), dur: time.Since(t0)})

	// 4. Freshness integrity — resolved vs unresolved. A DIRECT dep with no verified
	// Latest is UNRESOLVED (surfaced with ⚠; "couldn't verify" never collapses into
	// healthy). Indirect deps are managed transitively and never resolved, so they are
	// counted separately — NOT as unresolved, or they'd masquerade as failures (this is
	// the same classification skipReason applies to the Update box).
	resolved, unresolved, indirect := 0, 0, 0
	for _, d := range deps {
		switch {
		case d.Indirect:
			indirect++
		case d.ResolutionError != "" || d.Latest == "":
			unresolved++
		default:
			resolved++
		}
	}
	freshDetail := fmt.Sprintf("%d resolved · %d unresolved", resolved, unresolved)
	if indirect > 0 {
		freshDetail += fmt.Sprintf(" · %d indirect", indirect)
	}
	if unresolved > 0 {
		freshDetail += " ⚠"
	}
	steps = append(steps, depStep{label: "freshness", detail: freshDetail})

	// 5. Eligibility — candidates by ecosystem; ecosystems named ONLY when present.
	candidates, skipped := FilterUpdateCandidates(deps, cfg, trackedFiles)
	result.Skipped = skipped
	gomodDeps, dockerDeps, toolchainDeps, cargoDeps := groupByEcosystem(candidates)
	eligible := eligibleDetail(len(candidates), map[string]int{
		"gomod": len(gomodDeps), "docker": len(dockerDeps), "toolchain": len(toolchainDeps), "cargo": len(cargoDeps),
	})
	// "latest available" ≠ "autonomous-safe target": surface constraint-expanding
	// majors that are held for review rather than auto-applied.
	majorsHeld := 0
	for _, d := range deps {
		if d.MajorAvailable() {
			majorsHeld++
		}
	}
	if majorsHeld > 0 {
		eligible += fmt.Sprintf(" · %d major held (review)", majorsHeld)
	}
	steps = append(steps, depStep{label: "eligible", detail: eligible})
	if len(candidates) == 0 {
		return result, nil
	}

	// 6. Per-ecosystem activity — a row emerges ONLY for an ecosystem that had a
	// candidate, reporting what actually happened (updated / skipped). No zero rows.
	if len(gomodDeps) > 0 {
		t0 = time.Now()
		applied, goSkipped, touchedDirs, touchedFiles, err := applyGoUpdates(ctx, gomodDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "gomod", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying Go updates: %w", err)
		}
		steps = append(steps, ecosystemStep("gomod", applied, goSkipped, touchedDirs, time.Since(t0)))
		result.Applied = append(result.Applied, applied...)
		result.Skipped = append(result.Skipped, goSkipped...)
		result.TouchedModuleDirs = touchedDirs
		result.FilesChanged = append(result.FilesChanged, touchedFiles...)
	}

	if len(dockerDeps) > 0 {
		t0 = time.Now()
		applied, dkSkipped, touchedFiles, err := applyDockerfileUpdates(dockerDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "docker", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying Dockerfile updates: %w", err)
		}
		steps = append(steps, ecosystemStep("docker", applied, dkSkipped, nil, time.Since(t0)))
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
		steps = append(steps, ecosystemStep("toolchain", applied, tcSkipped, nil, time.Since(t0)))
		result.Applied = append(result.Applied, applied...)
		result.Skipped = append(result.Skipped, tcSkipped...)
		result.FilesChanged = append(result.FilesChanged, touchedFiles...)
	}

	if len(cargoDeps) > 0 {
		t0 = time.Now()
		applied, cgSkipped, touchedFiles, churn, err := applyCargoUpdates(ctx, cargoDeps, repoRoot)
		if err != nil {
			steps = append(steps, depStep{label: "cargo", status: "fail", detail: err.Error(), dur: time.Since(t0)})
			return result, fmt.Errorf("applying Cargo updates: %w", err)
		}
		steps = append(steps, cargoStep(applied, cgSkipped, churn, time.Since(t0)))
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
		// Behavioral verification (go test) now belongs to the test subsystem — the
		// cmd layer re-verifies the mutated tree via test.Verify (IntentDepReverify)
		// and gates the auto-commit on it. deps keeps only the vuln scan here for now.
		log, verifyErr := Verify(ctx, absDirs, repoRoot, false, cfg.Vulncheck)
		status, detail := "ok", "govulncheck"
		if verifyErr != nil {
			status, detail = "fail", "govulncheck — "+verifyErr.Error()
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
		// Layer C: StageFreight must never fail its own clean-tree gate on files
		// it generated this run. Its ephemeral namespace outputs are excluded;
		// user-owned working-tree cleanliness is still fully enforced. (This is
		// ONLY a skip — the .gitignore write / untrack happen at audition, not here.)
		if workspace.IsEphemeral(path) {
			continue
		}
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
