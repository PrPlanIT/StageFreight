package mirror

import "testing"

// ancestorOf builds an IsAncestor that reports true only for the given (ancestor,
// descendant) pairs — a deterministic stand-in for real commit-graph ancestry.
func ancestorOf(pairs map[[2]string]bool) func(string, string) bool {
	return func(a, d string) bool { return pairs[[2]string{a, d}] }
}

// E — a mirror ref whose commit is an ANCESTOR of the source commit is a fast-forward:
// a non-force Update, not a divergence.
func TestPlanRefs_FastForwardIsUpdateNotDiverged(t *testing.T) {
	src := map[string]string{"main": "descendant"}
	mir := map[string]string{"main": "ancestor"}
	p := PlanRefs(src, mir, RefOptions{
		IsAncestor: ancestorOf(map[[2]string]bool{{"ancestor", "descendant"}: true}),
	})
	if len(p.Diverged) != 0 {
		t.Fatalf("fast-forward must not be diverged, got %v", p.Diverged)
	}
	if len(p.Update) != 1 || p.Update[0].Ref != "main" {
		t.Fatalf("fast-forward must be an Update, got %+v", p.Update)
	}
	if p.Update[0].Force {
		t.Error("a fast-forward Update must be non-force")
	}
}

// E — a true fork (neither commit an ancestor of the other) stays diverged.
func TestPlanRefs_TrueForkIsDiverged(t *testing.T) {
	src := map[string]string{"main": "left"}
	mir := map[string]string{"main": "right"}
	p := PlanRefs(src, mir, RefOptions{
		IsAncestor: ancestorOf(nil), // no ancestry relationship
	})
	if !has(p.Diverged, "main") || len(p.Update) != 0 {
		t.Fatalf("a true fork must keep-divergent, got %+v", p)
	}
}

// E — a mirror commit absent from local history reports not-an-ancestor (the closure
// returns false), so it is treated as a divergence — no extra fetch needed.
func TestPlanRefs_MirrorCommitAbsentIsDiverged(t *testing.T) {
	src := map[string]string{"main": "known"}
	mir := map[string]string{"main": "absent"}
	// The real closure returns false when either commit is unresolvable locally.
	p := PlanRefs(src, mir, RefOptions{IsAncestor: func(string, string) bool { return false }})
	if !has(p.Diverged, "main") {
		t.Fatalf("an absent mirror commit must be diverged, got %+v", p)
	}
}

// E — force classification wins over fast-forward: an opted-in ref overwrites regardless
// of ancestry, and stays a FORCED update.
func TestPlanRefs_ForcePrecedesFastForward(t *testing.T) {
	src := map[string]string{"main": "descendant"}
	mir := map[string]string{"main": "ancestor"}
	isAnc := ancestorOf(map[[2]string]bool{{"ancestor", "descendant"}: true})

	force := PlanRefs(src, mir, RefOptions{Force: true, IsAncestor: isAnc})
	if len(force.Update) != 1 || !force.Update[0].Force {
		t.Fatalf("Force must win over FF and stay forced, got %+v", force.Update)
	}

	ref := PlanRefs(src, mir, RefOptions{
		ForceRef:   func(string) bool { return true },
		IsAncestor: isAnc,
	})
	if len(ref.Update) != 1 || !ref.Update[0].Force {
		t.Fatalf("ForceRef must win over FF and stay forced, got %+v", ref.Update)
	}
}

// E — nil IsAncestor preserves the ancestry-blind behavior: any differing ref diverges.
func TestPlanRefs_NilAncestorKeepsDivergedBehavior(t *testing.T) {
	src := map[string]string{"main": "new"}
	mir := map[string]string{"main": "old"}
	p := PlanRefs(src, mir, RefOptions{}) // IsAncestor nil
	if !has(p.Diverged, "main") || len(p.Update) != 0 {
		t.Fatalf("nil IsAncestor must keep the diverged default, got %+v", p)
	}
}

// A+E — a peeled-equal ref short-circuits to InSync BEFORE any ancestry check: the
// IsAncestor closure must never be consulted for an already-equal ref.
func TestPlanRefs_EqualShortCircuitsBeforeAncestry(t *testing.T) {
	src := map[string]string{"v1": "same"}
	mir := map[string]string{"v1": "same"}
	p := PlanRefs(src, mir, RefOptions{
		IsAncestor: func(string, string) bool {
			t.Fatal("ancestry must not be consulted for an equal (InSync) ref")
			return false
		},
	})
	if p.InSync != 1 || len(p.Update) != 0 || len(p.Diverged) != 0 {
		t.Fatalf("equal ref must be InSync, got %+v", p)
	}
}
