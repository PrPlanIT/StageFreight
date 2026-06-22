package disk

import (
	"fmt"
	"io"
	"strings"
)

// RenderOpts tunes child caps and bar width. Zero value = the default summary.
type RenderOpts struct {
	MaxKids  int // collapse a node's children beyond this into "+ N more" (0 → 8)
	BarWidth int // 0 → 16
}

func (o RenderOpts) maxKids() int {
	if o.MaxKids <= 0 {
		return 8
	}
	return o.MaxKids
}
func (o RenderOpts) barWidth() int {
	if o.BarWidth <= 0 {
		return 16
	}
	return o.BarWidth
}

// Render writes the full human diagnostic for r.
func Render(w io.Writer, host string, r *Report, opt RenderOpts) {
	total := r.FS.Total
	bw := opt.barWidth()

	fmt.Fprintf(w, "stagefreight du · %s", host)
	if total > 0 {
		fmt.Fprintf(w, "          disk %s  %s / %s used (%s)  %s",
			r.FS.Path, humanBytes(total-r.FS.Free), humanBytes(total),
			pctStr(total-r.FS.Free, total), barOf(total-r.FS.Free, total, bw))
	}
	fmt.Fprintln(w)

	for _, dom := range r.Domains {
		fmt.Fprintln(w)
		renderNode(w, dom, 0, total, bw, opt.maxKids())
	}

	if rows := r.ByProject(); len(rows) > 0 {
		fmt.Fprintf(w, "\n\nBY PROJECT\n")
		for _, row := range rows {
			line := fmt.Sprintf("  %-14s %9s  %s %5s   %s",
				row.Project, humanBytes(row.Bytes), barOf(row.Bytes, total, bw),
				pctStr(row.Bytes, total), projectParts(row))
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}

	if rec := r.Reclaimable(); len(rec) > 0 {
		var sum int64
		for _, n := range rec {
			sum += n.Bytes
		}
		fmt.Fprintf(w, "\n\nRECLAIM   ·   up to %s recoverable\n", humanBytes(sum))
		for _, n := range rec {
			if n.Bytes == 0 {
				continue // empty volumes/layers free nothing — don't clutter the ledger
			}
			cmd, safety := "", ""
			if n.Hint != nil {
				cmd, safety = n.Hint.Command, n.Hint.Safety
			}
			line := fmt.Sprintf("  %-26s %9s   %-26s %s",
				reclaimLabel(n), humanBytes(n.Bytes), cmd, safetyMark(safety))
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}

	fmt.Fprintf(w, "\n  -v subsystems   -vv per-artifact   --raw forensic   --by project|registry|runtime\n")
}

// renderNode prints n and its children, indented. Depth 0 = a domain header
// (LABEL + path). Children beyond maxKids collapse to a "+ N more" tail.
func renderNode(w io.Writer, n *Node, depth int, total int64, bw, maxKids int) {
	indent := strings.Repeat("  ", depth)
	label := n.Label
	if depth == 0 && n.Path != "" {
		label = n.Label + "  " + n.Path
	}
	name := truncate(indent+label, 42)
	row := fmt.Sprintf("  %-42s %9s  %s %5s  %s",
		name, humanBytes(n.Bytes), barOf(n.Bytes, total, bw), pctStr(n.Bytes, total), diag(n))
	fmt.Fprintln(w, strings.TrimRight(row, " "))

	kids := n.Kids
	var tail []*Node
	if len(kids) > maxKids {
		tail = kids[maxKids:]
		kids = kids[:maxKids]
	}
	for _, c := range kids {
		if c.Bytes == 0 {
			continue
		}
		renderNode(w, c, depth+1, total, bw, maxKids)
	}
	if len(tail) > 0 {
		var sum int64
		for _, t := range tail {
			sum += t.Bytes
		}
		ci := strings.Repeat("  ", depth+1)
		row := fmt.Sprintf("  %-42s %9s  %s %5s",
			ci+fmt.Sprintf("+ %d more", len(tail)), humanBytes(sum), barOf(sum, total, bw), pctStr(sum, total))
		fmt.Fprintln(w, strings.TrimRight(row, " "))
	}
}

// diag builds the trailing diagnosis: ⚠ leads, ♻ trails, the note in between.
func diag(n *Node) string {
	s := n.Note
	if n.Flags.Has(FlagAttention) {
		if s == "" {
			s = "⚠"
		} else {
			s = "⚠ " + s
		}
	}
	if n.Flags.Has(FlagReclaimable) {
		if s == "" {
			s = "♻"
		} else {
			s = s + " ♻"
		}
	}
	return s
}

func projectParts(row ProjectRow) string {
	var b []string
	for _, p := range row.Parts {
		b = append(b, fmt.Sprintf("%s %s", partLabel(p), humanBytesShort(p.Bytes)))
	}
	return strings.Join(b, " · ")
}

// partLabel describes a by-project contribution by its runtime/role.
func partLabel(n *Node) string {
	switch n.Attr.Runtime {
	case "cache-mount":
		return "rust-build"
	case "docker-host":
		return "host img"
	case "docker-dind":
		return "dind img"
	case "repo-tree":
		return "repo"
	default:
		return n.Label
	}
}

func reclaimLabel(n *Node) string {
	if n.Attr.Project != "" && n.Attr.Runtime == "cache-mount" {
		return "rust build · " + n.Attr.Project
	}
	return truncate(n.Label, 26)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n < 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

func safetyMark(s string) string {
	if s == "inspect first" {
		return "⚠ inspect first"
	}
	return s
}

// ── formatting ──────────────────────────────────────────────────────────────

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// humanBytesShort is the compact inline form: "8.1G", "684M".
func humanBytesShort(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(b)/float64(div), "KMGTPE"[exp])
}

func pctStr(b, total int64) string {
	if total <= 0 {
		return ""
	}
	p := float64(b) / float64(total) * 100
	if p < 1 {
		return "<1%"
	}
	return fmt.Sprintf("%d%%", int(p+0.5))
}

var barPartials = []rune("▏▎▍▌▋▊▉")

// barOf renders b as a share of total across width cells, partial-block precise.
func barOf(b, total int64, width int) string {
	if total <= 0 {
		return strings.Repeat("·", width)
	}
	cells := float64(b) / float64(total) * float64(width)
	full := int(cells)
	if full > width {
		full = width
	}
	out := make([]rune, 0, width)
	for i := 0; i < full; i++ {
		out = append(out, '█')
	}
	if full < width {
		if idx := int((cells - float64(full)) * 8); idx >= 1 {
			if idx > 7 {
				idx = 7
			}
			out = append(out, barPartials[idx-1])
		}
	}
	for len(out) < width {
		out = append(out, '·')
	}
	return string(out)
}
