package governance

import (
	"strings"
	"testing"
)

func msg(ref string) string {
	return buildCommitMessage(DistributionPlan{Files: []DistributedFile{
		{Path: ".stagefreight.yml", Action: "replace"},
	}}, "PrPlanIT/MaintenancePolicy", ref)
}

// The subject must be STABLE across reconciles. Carrying the source ref made every
// commit unique, so a satellite's log became a wall of near-identical lines that no
// viewer could collapse — and the ref is redundant, since the sealed file being
// committed states its own source repo and ref in its header.
func TestCommitSubjectIsStableAcrossReconciles(t *testing.T) {
	a := strings.SplitN(msg("643a4fdc2e87ac974407c3864a42f957fa9c56e5"), "\n", 2)[0]
	b := strings.SplitN(msg("2fcb480f6f7c686b6e35251e415249617752757b"), "\n", 2)[0]
	if a != b {
		t.Errorf("subject must not vary with the source ref:\n  %q\n  %q", a, b)
	}
	if strings.Contains(a, "643a4fdc") || len(a) > 60 {
		t.Errorf("subject must carry no ref and stay skimmable, got %q", a)
	}
}

// Provenance is not lost — it moves to the body, where it stays greppable.
func TestCommitBodyKeepsProvenance(t *testing.T) {
	m := msg("643a4fdc2e87ac974407c3864a42f957fa9c56e5")
	if !strings.Contains(m, "Source: PrPlanIT/MaintenancePolicy@643a4fdc") {
		t.Errorf("body must record the source, got:\n%s", m)
	}
}

// A tag or branch ref is a NAME: truncating it would corrupt it.
func TestShortRefOnlyAbbreviatesSHAs(t *testing.T) {
	for ref, want := range map[string]string{
		"643a4fdc2e87ac974407c3864a42f957fa9c56e5": "643a4fdc",
		"v1.2.3": "v1.2.3",
		"main":   "main",
		"release-2026-08-28-candidate-build-final": "release-2026-08-28-candidate-build-final",
	} {
		if got := shortRef(ref); got != want {
			t.Errorf("shortRef(%q) = %q, want %q", ref, got, want)
		}
	}
}
