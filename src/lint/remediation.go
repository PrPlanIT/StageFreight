package lint

import (
	"os"
	"path/filepath"
	"sort"
)

// RemediationSummary reports the outcome of an ApplyRemediations pass.
type RemediationSummary struct {
	FilesChanged int
	EditsApplied int
	Skipped      int            // edits skipped as out-of-range or overlapping (safety)
	Stale        int            // edits skipped because the file no longer holds Expected
	ByKind       map[string]int // applied edits per Kind
}

// ApplyRemediations writes every finding's Fix whose Kind is enabled back to disk. It is
// deliberately dumb: it performs exactly the byte spans the detectors emitted and
// re-derives nothing, so "what is fixed" equals "what was reported." Findings without a
// Fix, and Fixes whose Kind is not enabled, are left untouched — and because authored-
// hygiene modules never run on generated/vendored/lockfile content, no Fix exists for
// those files, so remediation is provenance-gated by construction.
//
// Two safety properties beyond the span itself:
//   - Compare-and-swap: an edit applies ONLY if the file still holds its Expected bytes,
//     so a stale finding against a since-changed file is skipped, never misapplied.
//   - Atomic write: each file is replaced via a temp file + fsync + rename, so a crash or
//     full disk mid-write can never leave a half-written (corrupted) source file.
//
// Edits to a file are applied high offset → low so earlier edits never shift later ones;
// overlapping or out-of-range spans are skipped rather than risked. Provide rootDir so
// relative finding paths resolve; file permissions are preserved.
func ApplyRemediations(findings []Finding, rootDir string, enabled map[string]bool) (RemediationSummary, error) {
	type edit struct {
		start, end     int
		expected, repl string
		kind           string
	}
	byFile := map[string][]edit{}
	for _, f := range findings {
		if f.Fix == nil || !enabled[f.Fix.Kind] {
			continue
		}
		byFile[f.File] = append(byFile[f.File], edit{f.Fix.Start, f.Fix.End, f.Fix.Expected, f.Fix.Replacement, f.Fix.Kind})
	}

	sum := RemediationSummary{ByKind: map[string]int{}}
	// Stable file order so the pass is deterministic.
	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)

	for _, file := range files {
		abs := filepath.Join(rootDir, file)
		info, err := os.Stat(abs)
		if err != nil {
			return sum, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return sum, err
		}

		edits := byFile[file]
		sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

		out := data
		applied := 0
		lastStart := len(data) + 1 // upper bound; each edit must end at or before this
		for _, e := range edits {
			if e.start < 0 || e.start > e.end || e.end > len(out) || e.end > lastStart {
				sum.Skipped++ // out of range or overlaps a higher edit — never guess
				continue
			}
			if string(out[e.start:e.end]) != e.expected {
				sum.Stale++ // file no longer holds the reported bytes — compare-and-swap fails
				continue
			}
			out = append(out[:e.start:e.start], append([]byte(e.repl), out[e.end:]...)...)
			lastStart = e.start
			applied++
			sum.ByKind[e.kind]++
		}

		if applied > 0 {
			if err := atomicWrite(abs, out, info.Mode().Perm()); err != nil {
				return sum, err
			}
			sum.FilesChanged++
			sum.EditsApplied += applied
		}
	}
	return sum, nil
}

// atomicWrite replaces path's contents durably: write to a temp file in the same
// directory, fsync it, then rename over the original (atomic on the same filesystem).
// A crash mid-write leaves the original intact and a stray temp file, never a truncated
// source. The parent directory is fsynced so the rename itself survives a crash.
func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sf-fix-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // harmless no-op once the rename has consumed it

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	// Best-effort: fsync the directory so the rename is durable across a crash.
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
