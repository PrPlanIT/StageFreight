package dependency

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// cargoRunner executes a cargo subcommand in a crate directory.
type cargoRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// resolveCargoRunner resolves a verified Rust toolchain via the SAME subsystem the
// rust build engine uses (official dist, checksum-verified, no host fallback, no
// container-for-tools) and returns a cargo runner with rustc/PATH wired for the
// standalone install.
// resolveCargoRunner is memoized per repo-root — resolve + render the provisioning
// ledger exactly ONCE across the deps run's phases (see resolveGoRunner).
var (
	cargoRunnerMu   sync.Mutex
	cargoRunnerMemo = map[string]cargoRunner{}
)

func resolveCargoRunner(repoRoot string) (cargoRunner, error) {
	cargoRunnerMu.Lock()
	defer cargoRunnerMu.Unlock()
	if r, ok := cargoRunnerMemo[repoRoot]; ok {
		return r, nil
	}
	version := toolchain.ResolveRustVersion(".", repoRoot)
	res, err := toolchain.Resolve(repoRoot, "rust", version)
	if err != nil {
		return nil, fmt.Errorf("rust toolchain: %w", err)
	}
	provision.Render(os.Stderr, []provision.Entry{provision.FromToolchain(res, "dependency update")}, output.UseColor())
	binDir := filepath.Dir(res.Path)
	runner := cargoRunner(func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, res.Path, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"RUSTC="+filepath.Join(binDir, "rustc"),
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		return cmd.CombinedOutput()
	})
	cargoRunnerMemo[repoRoot] = runner
	return runner, nil
}

// applyCargoUpdates applies Rust (Cargo.toml) version bumps with the same hash-guarded
// line-edit discipline as the Dockerfile updater, then runs `cargo update` to sync each
// crate's Cargo.lock. Returns applied/skipped plus touched files (Cargo.toml and, when
// the lock changes, Cargo.lock per crate).
func applyCargoUpdates(ctx context.Context, deps []supplychain.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, CargoChurn, error) {
	var applied []AppliedUpdate
	var skipped []SkippedDep

	// Build hash-guarded line edits, grouped by manifest file. dockerfileEdit is a
	// generic {dep,line,origHash,newLine} edit reused here (it is not Dockerfile-specific).
	type fileEdits struct {
		absPath string
		edits   []dockerfileEdit
	}
	byFile := make(map[string]*fileEdits)

	for _, dep := range deps {
		absPath := filepath.Join(repoRoot, dep.File)
		origLine, err := readLineAt(absPath, dep.Line)
		if err != nil {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceUnresolvable, Reason: fmt.Sprintf("cannot read line %d: %v", dep.Line, err)})
			continue
		}
		newLine, skip := buildCargoReplacement(dep, origLine)
		if skip != "" {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipSourceMismatch, Reason: skip})
			continue
		}
		if newLine == origLine {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: SkipNoChange, Reason: "no change after replacement"})
			continue
		}
		fe, ok := byFile[dep.File]
		if !ok {
			fe = &fileEdits{absPath: absPath}
			byFile[dep.File] = fe
		}
		fe.edits = append(fe.edits, dockerfileEdit{
			dep: dep, line: dep.Line, origHash: sha256.Sum256([]byte(origLine)), newLine: newLine,
		})
		target := dep.UpdateTarget()
		update := AppliedUpdate{Dep: dep, OldVer: dep.Current, NewVer: target, UpdateType: updateType(dep.Current, target)}
		for _, v := range dep.Vulnerabilities {
			update.CVEsFixed = append(update.CVEsFixed, v.ID)
		}
		applied = append(applied, update)
	}

	if len(byFile) == 0 {
		return applied, skipped, nil, CargoChurn{}, nil
	}

	// Edit the manifests (hash-verified).
	var touchedFiles []string
	for file, fe := range byFile {
		if err := applyFileEdits(fe.absPath, fe.edits); err != nil {
			return applied, skipped, nil, CargoChurn{}, fmt.Errorf("editing %s: %w", file, err)
		}
		touchedFiles = append(touchedFiles, file)
	}

	// Sync each crate's Cargo.lock to the new constraints. cargo update re-resolves the
	// lock and is the verification that the bumps resolve at all. A changed lock is a
	// touched file in the resulting MR.
	runCargo, err := resolveCargoRunner(repoRoot)
	if err != nil {
		return applied, skipped, deduplicateAndSort(touchedFiles), CargoChurn{}, err
	}

	// Intent fidelity: sync the lock for ONLY the packages we edited — never a blanket
	// re-resolve. A bare `cargo update` re-resolves the WHOLE graph to latest-semver for every
	// transitive, silently widening the change far beyond the declared intent: review scope and
	// blast radius balloon, and an unrelated transitive bump can break the build (a 3-package
	// intent once churned 141 lock entries and broke a musl target). Scope to
	// `cargo update -p <pkg>`, letting cargo move only the transitives it MUST to satisfy the
	// new constraints.
	//
	// Group edited package names by their update root: cargo update must run at the WORKSPACE
	// ROOT (members share one root Cargo.lock; a [patch] path crate is not a member, so a bare
	// `cargo update` inside it errors with "believes it's in a workspace when it's not"), and a
	// -p package only exists in its own root's lock.
	editedByDir := map[string][]string{}
	for file, fe := range byFile {
		dir := findCargoUpdateDir(repoRoot, file)
		for _, e := range fe.edits {
			editedByDir[dir] = appendUniqueStr(editedByDir[dir], e.dep.Name)
		}
	}
	var churn CargoChurn
	for dir, pkgs := range editedByDir {
		churn.Intended += len(pkgs)
		args := make([]string, 0, 1+2*len(pkgs))
		args = append(args, "update")
		for _, p := range pkgs {
			args = append(args, "-p", p)
		}
		out, err := runCargo(ctx, dir, args...)
		if err != nil {
			rel, _ := filepath.Rel(repoRoot, dir)
			return applied, skipped, deduplicateAndSort(touchedFiles), churn,
				fmt.Errorf("cargo update -p in %s: %w\n%s", rel, err, strings.TrimSpace(string(out)))
		}
		churn.Mutated += countLockMutations(out)
		lock := filepath.Join(dir, "Cargo.lock")
		if _, statErr := os.Stat(lock); statErr == nil {
			if rel, relErr := filepath.Rel(repoRoot, lock); relErr == nil {
				touchedFiles = append(touchedFiles, rel)
			}
		}
	}
	return applied, skipped, deduplicateAndSort(touchedFiles), churn, nil
}

// CargoChurn measures lock-sync intent fidelity: how many distinct packages we asked cargo
// to update (Intended) vs how many lockfile entries actually changed (Mutated). A ratio near
// 1 is healthy; a large ratio means a wide transitive re-resolve — operationally meaningful
// review signal, since it is exactly where unintended blast radius hides.
type CargoChurn struct {
	Intended int
	Mutated  int
}

// countLockMutations counts crate-level changes cargo reported — Updating/Adding/Removing/
// Downgrading lines that name a crate and a "vN" version — excluding registry/index refresh
// lines like "Updating crates.io index" or "Updating git repository `url`".
func countLockMutations(out []byte) int {
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		switch f[0] {
		case "Updating", "Adding", "Removing", "Downgrading":
			if v := f[2]; len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
				n++
			}
		}
	}
	return n
}

// appendUniqueStr appends s to xs only if not already present (small N, order-preserving).
func appendUniqueStr(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

// findCargoUpdateDir returns the directory `cargo update` should run in for a manifest: the
// nearest ancestor that declares a [workspace] (members and [patch] path crates share, and must
// be updated from, the workspace root that owns the single Cargo.lock), or the manifest's own
// directory if it is standalone.
func findCargoUpdateDir(repoRoot, manifestRel string) string {
	manifestDir := filepath.Dir(filepath.Join(repoRoot, manifestRel))
	for dir := manifestDir; strings.HasPrefix(dir, repoRoot); {
		if data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml")); err == nil && cargoDeclaresWorkspace(data) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return manifestDir
}

// cargoDeclaresWorkspace reports whether a Cargo.toml has a [workspace] table — i.e. is a
// workspace root — as opposed to a member that merely uses `workspace = true` dependencies.
func cargoDeclaresWorkspace(manifest []byte) bool {
	for _, line := range strings.Split(string(manifest), "\n") {
		t := strings.TrimSpace(line)
		if t == "[workspace]" || strings.HasPrefix(t, "[workspace.") {
			return true
		}
	}
	return false
}

// buildCargoReplacement swaps the pinned version in a Cargo.toml dependency line,
// e.g. `serde = "1.0.150"` or `tokio = { version = "1.0.150", features = [...] }`.
// Replaces the first occurrence of the current version so the crate name is never
// touched. Returns the new line + a skip reason (empty if eligible).
func buildCargoReplacement(dep supplychain.Dependency, origLine string) (string, string) {
	if dep.Current == "" {
		return origLine, "no current version to replace"
	}
	// Advance to the COMPATIBLE target, never the raw registry maximum — an
	// out-of-range major (e.g. reqwest 0.12 → 0.13, which renamed rustls-tls) would
	// break the manifest and is held for review instead.
	newLine := strings.Replace(origLine, dep.Current, dep.UpdateTarget(), 1)
	if newLine == origLine {
		return origLine, "current version not found in line"
	}
	return newLine, ""
}
