package gitplan

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

func TestPolicy_IsProtected(t *testing.T) {
	p := Policy{Protected: []string{"main", "release/*"}}
	cases := map[string]bool{
		"main": true, "release/1.2": true,
		"feature/x": false, "mainx": false, "release": false,
	}
	for br, want := range cases {
		if got := p.IsProtected(br); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", br, got, want)
		}
	}
}

func TestSituationFromState(t *testing.T) {
	p := Policy{Protected: []string{"main"}}

	// Clean ahead on a protected own-upstream → Upload only (solo push to main stays
	// Automatic; the governance only wakes on divergence).
	st := gitstate.RepoState{Branch: "main", RemoteName: "origin", UpstreamConfigured: true, AheadCount: 2}
	s := SituationFromState(st, p)
	if !s.Dest.Protected || s.Dest.Ref() != "origin/main" || s.Ahead != 2 || !s.HasUpstream {
		t.Fatalf("unexpected situation: %+v", s)
	}
	if got := kindsOf(Resolve(s)); len(got) != 1 || got[0] != OpUpload {
		t.Fatalf("clean ahead to protected own-upstream should Upload, got %v", got)
	}

	// Empty remote defaults to origin.
	st2 := gitstate.RepoState{Branch: "feature", UpstreamConfigured: false, AheadCount: 1}
	if SituationFromState(st2, p).Dest.Remote != "origin" {
		t.Fatal("empty remote should default to origin")
	}
}

// Layer 7 — golden render snapshot for the protected-replay case.
func TestRender_Golden(t *testing.T) {
	p := Resolve(Situation{Dest: Destination{Remote: "origin", Branch: "main", Protected: true}, HasUpstream: true, Ahead: 3, Behind: 2})
	want := "Plan: replay onto destination  [confirm]\n" +
		"  destination: origin/main\n" +
		"  confirm — replay 3 commit(s) onto origin/main — new commit IDs\n" +
		"  replay — origin/main\n" +
		"  upload\n"
	if got := Render(p); got != want {
		t.Fatalf("render mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
