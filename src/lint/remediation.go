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
	ByKind       map[string]int // applied edits per Kind
}

// ApplyRemediations writes every finding's Fix whose Kind is enabled back to disk. It is
// deliberately dumb: it performs exactly the byte spans the detectors emitted and
// re-derives nothing, so "what is fixed" equals "what was reported." Findings without a
// Fix, and Fixes whose Kind is not enabled, are left untouched — and because authored-
// hygiene modules never run on generated/vendored/lockfile content, no Fix exists for
// those files, so remediation is provenance-gated by construction.
//
// Edits to a file are applied high offset → low so earlier edits never shift later ones;
// overlapping or out-of-range spans are skipped rather than risked. Provide rootDir so
// relative finding paths resolve; file permissions are preserved.
func ApplyRemediations(findings []Finding, rootDir string, enabled map[string]bool) (RemediationSummary, error) {
	type edit struct {
		start, end int
		repl, kind string
	}
	byFile := map[string][]edit{}
	for _, f := range findings {
		if f.Fix == nil || !enabled[f.Fix.Kind] {
			continue
		}
		byFile[f.File] = append(byFile[f.File], edit{f.Fix.Start, f.Fix.End, f.Fix.Replacement, f.Fix.Kind})
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
			out = append(out[:e.start:e.start], append([]byte(e.repl), out[e.end:]...)...)
			lastStart = e.start
			applied++
			sum.ByKind[e.kind]++
		}

		if applied > 0 {
			if err := os.WriteFile(abs, out, info.Mode().Perm()); err != nil {
				return sum, err
			}
			sum.FilesChanged++
			sum.EditsApplied += applied
		}
	}
	return sum, nil
}
