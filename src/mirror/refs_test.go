package mirror

import (
	"strings"
	"testing"
)

func names(u []RefUpdate) []string {
	var n []string
	for _, x := range u {
		n = append(n, x.Ref)
	}
	return n
}
func has(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Missing source refs are created; matching ones are no-ops.
func TestRefs_CreateAndInSync(t *testing.T) {
	src := map[string]string{"main": "aaa", "release/1.0": "bbb"}
	mir := map[string]string{"main": "aaa"} // release/1.0 missing, main matches
	p := PlanRefs(src, mir, RefOptions{})
	if len(p.Create) != 1 || p.Create[0].Ref != "release/1.0" {
		t.Fatalf("create=%v", names(p.Create))
	}
	if p.InSync != 1 {
		t.Fatalf("InSync=%d", p.InSync)
	}
}

// A diverged ref is KEPT by default (never clobbered), forced only when asked.
func TestRefs_DivergentKeptByDefault(t *testing.T) {
	src := map[string]string{"main": "new"}
	mir := map[string]string{"main": "diverged"}

	keep := PlanRefs(src, mir, RefOptions{})
	if !has(keep.Diverged, "main") || len(keep.Update) != 0 {
		t.Fatalf("default should keep-divergent, got %+v", keep)
	}
	force := PlanRefs(src, mir, RefOptions{Force: true})
	if len(force.Update) != 1 || !force.Update[0].Force {
		t.Fatalf("force should overwrite, got %+v", force)
	}
}

// THE ref safety test: a contributor's branch (mirror-only, out of scope) is
// NEVER pruned, even under prune.
func TestRefs_ForeignBranchNeverPruned(t *testing.T) {
	scope := func(r string) bool { return r == "main" || strings.HasPrefix(r, "release/") }
	src := map[string]string{"main": "aaa"}
	mir := map[string]string{
		"main":           "aaa",
		"release/0.9":    "old", // ours, gone from source → prunable
		"feature/theirs": "zzz", // a contributor's branch, out of scope → foreign
	}
	p := PlanRefs(src, mir, RefOptions{Prune: true, InScope: scope})
	if len(p.Prune) != 1 || p.Prune[0].Ref != "release/0.9" {
		t.Fatalf("expected only release/0.9 pruned, got %v", names(p.Prune))
	}
	if !has(p.Foreign, "feature/theirs") {
		t.Fatal("contributor branch must be foreign/untouched")
	}
	if has(names(p.Prune), "feature/theirs") {
		t.Fatal("PRUNED A CONTRIBUTOR BRANCH — unacceptable")
	}
}

// Prune without a declared scope is a no-op: unattributable mirror-only refs are
// left alone (can't prove ownership → don't delete).
func TestRefs_PruneNeedsScope(t *testing.T) {
	src := map[string]string{"main": "aaa"}
	mir := map[string]string{"main": "aaa", "old-thing": "xxx"}
	p := PlanRefs(src, mir, RefOptions{Prune: true}) // no InScope
	if len(p.Prune) != 0 {
		t.Fatalf("prune with no scope must delete nothing, got %v", names(p.Prune))
	}
	if !has(p.Foreign, "old-thing") {
		t.Fatal("unattributable ref should be recorded foreign, not pruned")
	}
}

// Scope also gates mirroring: an out-of-scope source ref is not created.
func TestRefs_ScopeGatesMirroring(t *testing.T) {
	scope := func(r string) bool { return r == "main" }
	src := map[string]string{"main": "aaa", "wip/secret": "sss"}
	mir := map[string]string{}
	p := PlanRefs(src, mir, RefOptions{InScope: scope})
	if len(p.Create) != 1 || p.Create[0].Ref != "main" {
		t.Fatalf("only in-scope refs mirror, got %v", names(p.Create))
	}
}
