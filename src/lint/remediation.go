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
	Skipped      int            // edits skipped as overlapping within an otherwise-applied file
	Drifted      int            // files skipped ENTIRELY because content changed since the scan
	ByKind       map[string]int // applied edits per Kind
}

// ApplyRemediations writes every finding's Fix whose Kind is enabled back to disk. It is
// deliberately dumb: it performs exactly the byte spans the detectors emitted and
// re-derives nothing, so "what is fixed" equals "what was reported." Findings without a
// Fix, and Fixes whose Kind is not enabled, are left untouched — and because authored-
// hygiene modules never run on generated/vendored/lockfile content, no Fix exists for
// those files, so remediation is provenance-gated by construction.
//
// Three safety properties beyond the span itself:
//   - Compare-and-swap, transactional per file: every edit's Expected is verified against
//     the on-disk bytes BEFORE anything is written. If ANY span has drifted (content
//     changed, file shrank), the whole file is left untouched — a drifted file is never
//     partially remediated into a confusing mixed state. This makes the file-level
//     drift guard fall out of the per-span CAS: any change since the scan trips it.
//   - Atomic write: each file is replaced via a temp file + fsync + rename, so a crash or
//     full disk mid-write can never leave a half-written (corrupted) source file.
//   - dryRun: validate and count exactly what WOULD change, writing nothing.
//
// Within a non-drifted file, edits apply high offset → low so earlier edits never shift
// later ones; overlapping spans are skipped individually. Provide rootDir so relative
// finding paths resolve; file permissions are preserved.
func ApplyRemediations(findings []Finding, rootDir string, enabled map[string]bool, dryRun bool) (RemediationSummary, error) {
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

		// Phase 1 — drift gate (transactional): every edit's Expected must still match the
		// on-disk bytes. If any has drifted, the file changed since the scan; touch nothing.
		drifted := false
		for _, e := range edits {
			if e.start < 0 || e.start > e.end || e.end > len(data) || string(data[e.start:e.end]) != e.expected {
				drifted = true
				break
			}
		}
		if drifted {
			sum.Drifted++
			continue
		}

		// Phase 2 — apply high → low so offsets stay valid; overlaps skip individually.
		sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
		out := data
		applied := 0
		lastStart := len(data) + 1
		for _, e := range edits {
			if e.end > lastStart { // overlaps a higher edit already applied
				sum.Skipped++
				continue
			}
			out = append(out[:e.start:e.start], append([]byte(e.repl), out[e.end:]...)...)
			lastStart = e.start
			applied++
			sum.ByKind[e.kind]++
		}

		if applied > 0 {
			if !dryRun {
				if err := atomicWrite(abs, out, info.Mode().Perm()); err != nil {
					return sum, err
				}
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
