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
var worldModules = map[string]bool{"freshness": true, "osv": true}

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
	for _, f := range current {
		if worldModules[f.Module] {
			continue // world-driven: never "introduced by this change"
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	newFp := map[string]bool{}
	if len(byFile) == 0 {
		return newFp, nil
	}

	tmp, err := os.MkdirTemp("", "sf-baseline-*")
	if err != nil {
		return newFp, err
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
		return newFp, nil
	}

	// One base lint pass over the staged tree, world modules skipped.
	skip := make([]string, 0, len(worldModules))
	for m := range worldModules {
		skip = append(skip, m)
	}
	baseEngine, err := NewEngine(cfg, tmp, nil, skip, false, cache)
	if err != nil {
		return newFp, err
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
		return newFp, err
	}

	baseFp := map[string]bool{}
	for _, f := range baseFindings {
		if !worldModules[f.Module] {
			baseFp[f.Fingerprint()] = true
		}
	}

	// A current finding on a changed file is new iff its fingerprint is absent at base.
	for path := range staged {
		for _, f := range byFile[path] {
			if !baseFp[f.Fingerprint()] {
				newFp[f.Fingerprint()] = true
			}
		}
	}
	return newFp, nil
}
