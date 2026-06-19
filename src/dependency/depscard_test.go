package dependency

import (
	"strings"
	"testing"
)

func TestEligibleDetail_NamesOnlyPresentEcosystems(t *testing.T) {
	if d := eligibleDetail(0, nil); d != "none" {
		t.Errorf("zero candidates → none, got %q", d)
	}
	// Only ecosystems with candidates appear — no zero slots.
	if d := eligibleDetail(1, map[string]int{"gomod": 1, "cargo": 0, "docker": 0}); d != "1 candidate (gomod)" {
		t.Errorf("got %q", d)
	}
	if d := eligibleDetail(3, map[string]int{"gomod": 1, "cargo": 2}); d != "3 candidates (gomod, cargo)" {
		t.Errorf("got %q", d)
	}
}

func TestEcosystemStep_EventOriented(t *testing.T) {
	// Skipped (the content-module case) renders as a skip event, never healthy.
	s := ecosystemStep("gomod", nil, []SkippedDep{{Reason: "no Go source (content/tooling module)"}}, nil, 0)
	if s.status != "skip" || !strings.Contains(s.detail, "no Go source") {
		t.Errorf("skipped ecosystem: status=%q detail=%q", s.status, s.detail)
	}
	// Updated.
	a := ecosystemStep("cargo", []AppliedUpdate{{}, {}}, nil, nil, 0)
	if a.status != "ok" || !strings.Contains(a.detail, "updated 2") {
		t.Errorf("applied ecosystem: status=%q detail=%q", a.status, a.detail)
	}
}
