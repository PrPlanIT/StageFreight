package lint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// worldModules produce findings that track EXTERNAL state (CVE feeds, upstream release
// versions), not the code under review. "Newly introduced by this change" is meaningless
// for them — a new CVE appears because the world moved, not because the diff did — so they
// are excluded from baseline finding diffs (and skipped in the base re-lint, which also
// avoids their network calls).
var worldModules = map[string]bool{"freshness": true, "osv": true, "vulnerabilities": true}

// NewFindings returns the set of fingerprints in `current` that are newly introduced
// relative to the baseline. For each changed file it lints the baseline version and diffs
// by fingerprint (line-independent identity), so a moved or reworded finding is NOT new.
// Unchanged files contribute nothing (same bytes → same findings → same fingerprints).
// A file absent at the baseline is new, so all its findings are new.
//
// It degrades safely: any error returns what was computed so far with the error, and the
// caller treats a baseline-diff failure as "no diff", never as a failed lint.
func (b *Baseline) NewFindings(current []Finding, cfg config.LintConfig, rootDir string, cache *Cache) (map[string]bool, error) {
	byFile := map[string][]Finding{}
	var fullRepo []Finding
	for _, f := range current {
		switch {
		case worldModules[f.Module]:
			continue // world-driven: never "introduced by this change"
		case isFullRepoModuleName(f.Module):
			// A whole-repo finding (e.g. a broken link) can reference a file that did
			// not change, so it cannot be diffed against a single file's base content —
			// it is diffed against the whole base tree below.
			fullRepo = append(fullRepo, f)
		default:
			byFile[f.File] = append(byFile[f.File], f)
		}
	}
	newFp := map[string]bool{}
	if err := b.markNewPerFile(byFile, cfg, rootDir, cache, newFp); err != nil {
		return newFp, err
	}
	if err := b.markNewFullRepo(fullRepo, cfg, cache, newFp); err != nil {
		return newFp, err
	}
	return newFp, nil
}

// markNewPerFile diffs ordinary (per-file) findings against the baseline version of each
// changed file: a finding is new iff its fingerprint is absent when that file's base content
// is re-linted. Unchanged files contribute nothing; a file absent at the baseline has all
// its findings marked new. Whole-repo and world modules are excluded from this pass — the
// first because a partial staged tree cannot represent it, the second because "new" is
// meaningless for world-driven findings.
func (b *Baseline) markNewPerFile(byFile map[string][]Finding, cfg config.LintConfig, rootDir string, cache *Cache, newFp map[string]bool) error {
	if len(byFile) == 0 {
		return nil
	}

	tmp, err := os.MkdirTemp("", "sf-baseline-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	staged := map[string]bool{} // changed files (base exists, content differs) needing a base lint
	for path, fs := range byFile {
		baseContent, ok, cerr := b.Content(path)
		if cerr != nil {
			continue // can't read base → leave findings unmarked (conservative)
		}
		if !ok {
			// File didn't exist at baseline → every finding on it is new.
			for _, f := range fs {
				newFp[f.Fingerprint()] = true
			}
			continue
		}
		if cur, rerr := os.ReadFile(filepath.Join(rootDir, path)); rerr == nil && bytes.Equal(cur, baseContent) {
			continue // unchanged → no new findings
		}
		dst := filepath.Join(tmp, path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(dst, baseContent, 0o644); err != nil {
			continue
		}
		staged[path] = true
	}

	if len(staged) == 0 {
		return nil
	}

	// One base lint pass over the staged tree, world AND whole-repo modules skipped
	// (whole-repo modules are handled separately against the full base tree).
	skip := append(worldModuleNames(), fullRepoModuleNames()...)
	baseEngine, err := NewEngine(cfg, tmp, nil, skip, false, cache)
	if err != nil {
		return err
	}
	baseFiles := make([]FileInfo, 0, len(staged))
	for path := range staged {
		abs := filepath.Join(tmp, path)
		var size int64
		if info, serr := os.Stat(abs); serr == nil {
			size = info.Size()
		}
		baseFiles = append(baseFiles, FileInfo{Path: path, AbsPath: abs, Size: size})
	}
	baseFindings, _, err := baseEngine.RunWithStats(context.Background(), baseFiles)
	if err != nil {
		return err
	}

	baseFp := map[string]bool{}
	for _, f := range baseFindings {
		baseFp[f.Fingerprint()] = true
	}

	// A current finding on a changed file is new iff its fingerprint is absent at base.
	for path := range staged {
		for _, f := range byFile[path] {
			if !baseFp[f.Fingerprint()] {
				newFp[f.Fingerprint()] = true
			}
		}
	}
	return nil
}

// markNewFullRepo diffs whole-repo (FullRepoModule) findings against the baseline version of
// the ENTIRE repo. A link checker cannot be baselined per changed file — a link's target may
// be a file that did not itself change — so the base version of every path is staged and the
// full-repo modules are re-linted over that whole base tree; a current finding is new iff its
// fingerprint is absent there. This is what makes `--baseline` both surface pre-existing link
// debt without blocking AND still block a link this change actually broke.
func (b *Baseline) markNewFullRepo(fullRepo []Finding, cfg config.LintConfig, cache *Cache, newFp map[string]bool) error {
	if len(fullRepo) == 0 {
		return nil
	}

	basePaths, err := b.Paths()
	if err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "sf-baseline-full-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	staged := 0
	for path := range basePaths {
		content, ok, cerr := b.Content(path)
		if cerr != nil || !ok {
			continue
		}
		dst := filepath.Join(tmp, path)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			continue
		}
		staged++
	}
	if staged == 0 {
		// No base tree to compare against → the whole repo is new → every finding is new.
		for _, f := range fullRepo {
			newFp[f.Fingerprint()] = true
		}
		return nil
	}

	only := fullRepoModuleNames()
	baseEngine, err := NewEngine(cfg, tmp, only, nil, false, cache)
	if err != nil {
		return err
	}
	// CollectFiles populates the engine's full set, so each full-repo module sees the whole
	// base tree (not a delta) — the same completeness the main run gives it.
	baseFiles, err := baseEngine.CollectFiles()
	if err != nil {
		return err
	}
	baseFindings, _, err := baseEngine.RunWithStats(context.Background(), baseFiles)
	if err != nil {
		return err
	}

	baseFp := map[string]bool{}
	for _, f := range baseFindings {
		baseFp[f.Fingerprint()] = true
	}
	for _, f := range fullRepo {
		if !baseFp[f.Fingerprint()] {
			newFp[f.Fingerprint()] = true
		}
	}
	return nil
}

// worldModuleNames / fullRepoModuleNames list the module names excluded from the per-file
// base pass. isFullRepoModuleName reports whether a registered module needs the whole repo.
func worldModuleNames() []string {
	names := make([]string, 0, len(worldModules))
	for m := range worldModules {
		names = append(names, m)
	}
	return names
}

func fullRepoModuleNames() []string {
	var names []string
	for _, name := range All() {
		if isFullRepoModuleName(name) {
			names = append(names, name)
		}
	}
	return names
}

func isFullRepoModuleName(name string) bool {
	m, err := Get(name)
	if err != nil {
		return false
	}
	fr, ok := m.(FullRepoModule)
	return ok && fr.NeedsFullRepo()
}
