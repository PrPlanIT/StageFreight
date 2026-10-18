// Package merge is a three-way merge of text and of git trees, in pure Go: the runtime image
// carries no git binary. It merges the way diff3 does — changes from both sides that touch
// different lines of the common base are combined; changes that overlap are a conflict unless
// both sides made the identical change. It never guesses at an overlap.
package merge

import (
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"
)

// hunk is one change one side made to the base: base lines [start, end) became lines.
type hunk struct {
	start, end int
	lines      []string
}

// splitLines splits text into lines that keep their terminators, so a merge reproduces line
// endings exactly.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// hunks lists the changes that turn base into other, in base order.
func hunks(base, other []string) []hunk {
	dmp := diffmatchpatch.New()
	a, b, lines := dmp.DiffLinesToChars(strings.Join(base, ""), strings.Join(other, ""))
	diffs := dmp.DiffCharsToLines(dmp.DiffMain(a, b, false), lines)
	var out []hunk
	pos := 0
	var cur *hunk
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for _, d := range diffs {
		n := len(splitLines(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			flush()
			pos += n
		case diffmatchpatch.DiffDelete:
			if cur == nil {
				cur = &hunk{start: pos, end: pos}
			}
			cur.end += n
			pos += n
		case diffmatchpatch.DiffInsert:
			if cur == nil {
				cur = &hunk{start: pos, end: pos}
			}
			cur.lines = append(cur.lines, splitLines(d.Text)...)
		}
	}
	flush()
	return out
}

// Text merges ours and theirs against their common base. ok is false when the two sides made
// different changes to overlapping (or touching) parts of the base; the result is then not
// meaningful and must not be used.
func Text(base, ours, theirs string) (merged string, ok bool) {
	if ours == theirs {
		return ours, true
	}
	if base == ours {
		return theirs, true
	}
	if base == theirs {
		return ours, true
	}
	b := splitLines(base)
	oh, th := hunks(b, splitLines(ours)), hunks(b, splitLines(theirs))
	var out []string
	pos, i, j := 0, 0, 0
	for i < len(oh) || j < len(th) {
		// Open a region at the next change in base order, then pull in every change from
		// either side that overlaps or touches it (closed intervals: an edit adjacent to the
		// other side's edit is a conflict, as in git). Hunks of one side never touch each
		// other — the diff separates them by at least one unchanged line.
		var os, ts []hunk
		var start, end int
		if j >= len(th) || (i < len(oh) && oh[i].start <= th[j].start) {
			start, end = oh[i].start, oh[i].end
			os = append(os, oh[i])
			i++
		} else {
			start, end = th[j].start, th[j].end
			ts = append(ts, th[j])
			j++
		}
		for {
			switch {
			case i < len(oh) && oh[i].start <= end:
				end = max(end, oh[i].end)
				os = append(os, oh[i])
				i++
			case j < len(th) && th[j].start <= end:
				end = max(end, th[j].end)
				ts = append(ts, th[j])
				j++
			default:
				goto region
			}
		}
	region:
		out = append(out, b[pos:start]...)
		switch {
		case len(ts) == 0:
			out = append(out, apply(b, start, end, os)...)
		case len(os) == 0:
			out = append(out, apply(b, start, end, ts)...)
		default:
			o, t := apply(b, start, end, os), apply(b, start, end, ts)
			if strings.Join(o, "") != strings.Join(t, "") {
				return "", false
			}
			out = append(out, o...)
		}
		pos = end
	}
	out = append(out, b[pos:]...)
	return strings.Join(out, ""), true
}

// apply renders base [start, end) with one side's hunks in that region applied.
func apply(base []string, start, end int, hs []hunk) []string {
	var out []string
	pos := start
	for _, h := range hs {
		out = append(out, base[pos:h.start]...)
		out = append(out, h.lines...)
		pos = h.end
	}
	return append(out, base[pos:end]...)
}
