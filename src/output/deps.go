package output

import (
	"fmt"
	"sort"
	"strings"
)

const (
	MaxApplied         = 20
	MaxCVEs            = 12
	MaxSkippedPerGroup = 5
)

// AppliedDep is the view model for a single applied update.
type AppliedDep struct {
	Name       string
	OldVer     string
	NewVer     string
	UpdateType string   // "major", "minor", "patch", "tag"
	CVEsFixed  []string // IDs only
}

// SkippedGroup is a pre-aggregated skip summary entry.
type SkippedGroup struct {
	Reason string
	Count  int
}

// CVEFixed is the view model for a vulnerability resolved by an update.
type CVEFixed struct {
	ID       string // "CVE-2024-45337", "GHSA-xxxx-yyyy-zzzz"
	Severity string // "LOW", "MODERATE", "HIGH", "CRITICAL"
	Summary  string
	FixedIn  string // "v0.37.0"
	FixedBy  string // "golang.org/x/crypto"
}

// SectionApplied renders the "Applied (N)" or "Would update (N)" block.
func SectionApplied(sec *Section, header string, updates []AppliedDep, color bool) {
	if len(updates) == 0 {
		return
	}

	sec.Row("")
	sec.Row("%s", bold(color, fmt.Sprintf("%s (%d)", header, len(updates))))

	show := len(updates)
	if show > MaxApplied {
		show = MaxApplied
	}

	for i := 0; i < show; i++ {
		u := updates[i]
		sec.Row("  %s", strings.TrimSpace(u.Name))

		line := fmt.Sprintf("    %s → %s", strings.TrimSpace(u.OldVer), strings.TrimSpace(u.NewVer))
		if typ := strings.TrimSpace(u.UpdateType); typ != "" {
			line += "  " + Dimmed(typ, color)
		}
		sec.Row("%s", line)

		if len(u.CVEsFixed) > 0 {
			sec.Row("%s", Dimmed("    fixes "+strings.Join(u.CVEsFixed, ", "), color))
		}
	}

	if len(updates) > MaxApplied {
		remaining := len(updates) - MaxApplied
		sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more (see resolve.json)", remaining), color))
	}

	sec.Row("")
}

// SkippedItem is the view model for a single skipped dependency.
type SkippedItem struct {
	Name      string
	Version   string
	Ecosystem string
}

// SkippedItemGroup is a skip summary with itemized dependency details.
type SkippedItemGroup struct {
	Reason string
	Count  int
	Items  []SkippedItem
}

// SectionSkippedItemized renders the "Skipped (N)" block with per-item detail.
func SectionSkippedItemized(sec *Section, header string, groups []SkippedItemGroup, color bool) {
	if len(groups) == 0 {
		return
	}

	total := 0
	for _, g := range groups {
		total += g.Count
	}

	sec.Row("")
	sec.Row("%s", bold(color, fmt.Sprintf("%s (%d)", header, total)))

	// Sort by count desc, then reason asc.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Reason < groups[j].Reason
	})

	for _, g := range groups {
		sec.Row("  %-22s %d", g.Reason, g.Count)

		show := len(g.Items)
		if show > MaxSkippedPerGroup {
			show = MaxSkippedPerGroup
		}

		for i := 0; i < show; i++ {
			item := g.Items[i]
			detail := item.Name
			if item.Version != "" {
				detail += " " + item.Version
			}
			if item.Ecosystem != "" {
				detail += " (" + item.Ecosystem + ")"
			}
			sec.Row("%s", Dimmed("    "+detail, color))
		}

		if len(g.Items) > MaxSkippedPerGroup {
			remaining := len(g.Items) - MaxSkippedPerGroup
			sec.Row("%s", Dimmed(fmt.Sprintf("    … and %d more (see resolve.json)", remaining), color))
		}
	}

	sec.Row("")
}

// SectionSkipped renders the "Skipped (N)" or "Would skip (N)" block (pre-aggregated).
func SectionSkipped(sec *Section, header string, groups []SkippedGroup, color bool) {
	if len(groups) == 0 {
		return
	}

	total := 0
	for _, g := range groups {
		total += g.Count
	}

	sec.Row("")
	sec.Row("%s", bold(color, fmt.Sprintf("%s (%d)", header, total)))

	// Sort by count desc, then reason asc.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Count != groups[j].Count {
			return groups[i].Count > groups[j].Count
		}
		return groups[i].Reason < groups[j].Reason
	})

	for _, g := range groups {
		sec.Row("  %-22s %d", g.Reason, g.Count)
	}

	sec.Row("")
}

// SectionCVEs renders the "CVEs Fixed (N)" table (truncates at MaxCVEs).
func SectionCVEs(sec *Section, cves []CVEFixed, color bool) {
	if len(cves) == 0 {
		return
	}

	sec.Row("")
	sec.Row("%s", bold(color, fmt.Sprintf("CVEs Fixed (%d)", len(cves))))

	show := len(cves)
	if show > MaxCVEs {
		show = MaxCVEs
	}

	for i := 0; i < show; i++ {
		c := cves[i]
		tag := VulnSeverityTag(c.Severity, color)

		sec.Row("  %-14s  %-4s  %s", strings.TrimSpace(c.ID), tag, strings.TrimSpace(c.Summary))
		sec.Row("%s", Dimmed(fmt.Sprintf("    fixed by %s %s", strings.TrimSpace(c.FixedBy), strings.TrimSpace(c.FixedIn)), color))

		if id := strings.TrimSpace(c.ID); id != "" {
			sec.Row("%s", Dimmed("    "+VulnURL(id), color))
		}
	}

	if len(cves) > MaxCVEs {
		remaining := len(cves) - MaxCVEs
		sec.Row("%s", Dimmed(fmt.Sprintf("  … and %d more (see deps-report.md)", remaining), color))
	}

	sec.Row("")
}
