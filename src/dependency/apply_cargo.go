package dependency

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// cargoRunner executes a cargo subcommand in a crate directory.
type cargoRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// resolveCargoRunner resolves a verified Rust toolchain via the SAME subsystem the
// rust build engine uses (official dist, checksum-verified, no host fallback, no
// container-for-tools) and returns a cargo runner with rustc/PATH wired for the
// standalone install.
func resolveCargoRunner(repoRoot string) (cargoRunner, error) {
	version := toolchain.ResolveRustVersion(".", repoRoot)
	res, err := toolchain.Resolve(repoRoot, "rust", version)
	if err != nil {
		return nil, fmt.Errorf("rust toolchain: %w", err)
	}
	toolchain.Report(os.Stderr, res)
	binDir := filepath.Dir(res.Path)
	return func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, res.Path, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"RUSTC="+filepath.Join(binDir, "rustc"),
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		)
		return cmd.CombinedOutput()
	}, nil
}

// applyCargoUpdates applies Rust (Cargo.toml) version bumps with the same hash-guarded
// line-edit discipline as the Dockerfile updater, then runs `cargo update` to sync each
// crate's Cargo.lock. Returns applied/skipped plus touched files (Cargo.toml and, when
// the lock changes, Cargo.lock per crate).
func applyCargoUpdates(ctx context.Context, deps []freshness.Dependency, repoRoot string) ([]AppliedUpdate, []SkippedDep, []string, error) {
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
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: fmt.Sprintf("cannot read line %d: %v", dep.Line, err)})
			continue
		}
		newLine, skip := buildCargoReplacement(dep, origLine)
		if skip != "" {
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: skip})
			continue
		}
		if newLine == origLine {
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: "no change after replacement"})
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
		return applied, skipped, nil, nil
	}

	// Edit the manifests (hash-verified).
	var touchedFiles []string
	for file, fe := range byFile {
		if err := applyFileEdits(fe.absPath, fe.edits); err != nil {
			return applied, skipped, nil, fmt.Errorf("editing %s: %w", file, err)
		}
		touchedFiles = append(touchedFiles, file)
	}

	// Sync each crate's Cargo.lock to the new constraints. cargo update re-resolves the
	// lock and is the verification that the bumps resolve at all. A changed lock is a
	// touched file in the resulting MR.
	runCargo, err := resolveCargoRunner(repoRoot)
	if err != nil {
		return applied, skipped, deduplicateAndSort(touchedFiles), err
	}
	// cargo update must run at the WORKSPACE ROOT, not in each edited sub-manifest. A workspace
	// shares one Cargo.lock at its root, and a [patch] path crate (e.g. patches/proxmox-client)
	// is NOT a workspace member — `cargo update` inside it fails with "believes it's in a
	// workspace when it's not". Resolve each edited manifest to its update root, dedupe, and run
	// once per distinct root (which also re-resolves the patched/member crates that were edited).
	updateDirs := map[string]bool{}
	for file := range byFile {
		updateDirs[findCargoUpdateDir(repoRoot, file)] = true
	}
	for dir := range updateDirs {
		if out, err := runCargo(ctx, dir, "update"); err != nil {
			rel, _ := filepath.Rel(repoRoot, dir)
			return applied, skipped, deduplicateAndSort(touchedFiles),
				fmt.Errorf("cargo update in %s: %w\n%s", rel, err, strings.TrimSpace(string(out)))
		}
		lock := filepath.Join(dir, "Cargo.lock")
		if _, statErr := os.Stat(lock); statErr == nil {
			if rel, relErr := filepath.Rel(repoRoot, lock); relErr == nil {
				touchedFiles = append(touchedFiles, rel)
			}
		}
	}
	return applied, skipped, deduplicateAndSort(touchedFiles), nil
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
func buildCargoReplacement(dep freshness.Dependency, origLine string) (string, string) {
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
