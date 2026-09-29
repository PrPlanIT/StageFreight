package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// ── sync reporting ────────────────────────────────────────────────────────
//
// Sync renders a framed section like every other subsystem (Release, Badges,
// Scribe) instead of loose prints below them. Rows are buffered because a
// section writes its header — elapsed included — before its content.
//
// Bounding rule: EVERY field is a count or a capped list. Nothing rendered here
// may grow with the size of the repository. A refspec list, an unbounded tag
// list or a raw plan dump turns one scannable line into hundreds of characters
// on a repo with many refs, which is how this surface got broken before.

// maxListedTags caps any per-tag enumeration; the remainder is summarised.
const maxListedTags = 5

type syncRow struct {
	icon   string // output.StatusIcon status word, or "" for a continuation row
	facet  string // refs | releases | retention | "" for a continuation row
	detail string
}

type syncMirrorReport struct {
	id   string
	rows []syncRow
}

func (r *syncMirrorReport) add(icon, facet, format string, args ...any) {
	r.rows = append(r.rows, syncRow{icon: icon, facet: facet, detail: fmt.Sprintf(format, args...)})
}

// note adds a hanging continuation row under the facet above it.
func (r *syncMirrorReport) note(format string, args ...any) {
	r.rows = append(r.rows, syncRow{detail: fmt.Sprintf(format, args...)})
}

// capList renders up to maxListedTags names, summarising any remainder, so an
// enumeration can never scale with the repository.
func capList(names []string) string {
	if len(names) <= maxListedTags {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(names[:maxListedTags], ", "), len(names)-maxListedTags)
}

// planDetail renders a push plan as fixed-width counts.
func planDetail(refspecs, pruned, foreign int) string {
	return fmt.Sprintf("%d refspecs · %d pruned · %d foreign kept", refspecs, pruned, foreign)
}

// render writes the buffered reports as one framed Sync section. Nothing is
// emitted when no mirror produced a row, so a no-op sync stays silent.
func renderSyncSection(w interface {
	Write([]byte) (int, error)
}, reports []syncMirrorReport, elapsed time.Duration, degraded bool) {
	total := 0
	for _, r := range reports {
		total += len(r.rows)
	}
	if total == 0 {
		return
	}
	color := output.UseColor()
	output.SectionStart(w, "sf_sync", "Sync")
	sec := output.NewSection(w, "Sync", elapsed, color)
	for _, r := range reports {
		sec.Row("%s", r.id)
		for _, row := range r.rows {
			if row.icon == "" {
				// Continuation rows carry their own label; indent to the facet column
				// so the label lines up under the facet above it.
				sec.RowIndented(5, true, color, "%s", row.detail)
				continue
			}
			sec.RowIndented(2, false, color, "%s  %-10s %s", output.StatusIcon(row.icon, color), row.facet, row.detail)
		}
	}
	if degraded {
		sec.Separator()
		sec.Row("%s replication degraded — one or more mirrors failed", output.StatusIcon("warning", color))
	}
	sec.Close()
	output.SectionEnd(w, "sf_sync")
}

// resolveTagCommit resolves a tag to its commit SHA in the local worktree, peeling
// an annotated tag to its target. Used to anchor a mirror release to the real commit
// instead of letting the forge default a created tag to its own branch HEAD.
func resolveTagCommit(worktree, tag string) (string, error) {
	repo, err := gitstate.OpenRepo(worktree)
	if err != nil {
		return "", err
	}
	return gitstate.ResolveRef(repo, tag)
}
