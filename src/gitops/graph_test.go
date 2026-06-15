package gitops

import (
	"reflect"
	"testing"
)

// mkGraph builds a FluxGraph from nodes, deriving ReverseDeps the same way
// DiscoverFluxGraph does, so ReconcileOrder sees a consistent graph.
func mkGraph(nodes ...KustomizationNode) *FluxGraph {
	g := &FluxGraph{
		Kustomizations: map[KustomizationKey]KustomizationNode{},
		ReverseDeps:    map[KustomizationKey][]KustomizationKey{},
	}
	for _, n := range nodes {
		g.Kustomizations[n.Key] = n
	}
	for _, n := range nodes {
		for _, d := range n.DependsOn {
			g.ReverseDeps[d] = append(g.ReverseDeps[d], n.Key)
		}
	}
	return g
}

func k(name string) KustomizationKey { return KustomizationKey{Namespace: "flux-system", Name: name} }

func node(name, path string, deps ...string) KustomizationNode {
	n := KustomizationNode{Key: k(name), Path: path}
	for _, d := range deps {
		n.DependsOn = append(n.DependsOn, k(d))
	}
	return n
}

func TestReconcileOrder_DependenciesFirst(t *testing.T) {
	// c depends on b depends on a → a, b, c.
	g := mkGraph(
		node("c", "c", "b"),
		node("a", "a"),
		node("b", "b", "a"),
	)
	got := ReconcileOrder(g)
	want := []KustomizationKey{k("a"), k("b"), k("c")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReconcileOrder = %v, want %v", got, want)
	}
}

func TestReconcileOrder_DeterministicLexicalTiebreak(t *testing.T) {
	// Independent roots must come out lexically, regardless of map iteration.
	g := mkGraph(node("z", "z"), node("a", "a"), node("m", "m"))
	want := []KustomizationKey{k("a"), k("m"), k("z")}
	for i := 0; i < 25; i++ {
		got := ReconcileOrder(g)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: ReconcileOrder = %v, want %v", i, got, want)
		}
	}
}

func TestReconcileOrder_DiamondDepsBeforeDependents(t *testing.T) {
	// infra <- {apps, monitoring} <- platform
	g := mkGraph(
		node("platform", "p", "apps", "monitoring"),
		node("apps", "a", "infra"),
		node("monitoring", "m", "infra"),
		node("infra", "i"),
	)
	got := ReconcileOrder(g)
	pos := map[string]int{}
	for i, key := range got {
		pos[key.Name] = i
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 nodes, got %v", got)
	}
	for _, pair := range [][2]string{{"infra", "apps"}, {"infra", "monitoring"}, {"apps", "platform"}, {"monitoring", "platform"}} {
		if pos[pair[0]] >= pos[pair[1]] {
			t.Errorf("%s must reconcile before %s, got order %v", pair[0], pair[1], got)
		}
	}
}

func TestReconcileOrder_CycleStillCoversAll(t *testing.T) {
	// a <-> b cycle plus independent c: must not hang, must cover all three.
	g := mkGraph(node("a", "a", "b"), node("b", "b", "a"), node("c", "c"))
	got := ReconcileOrder(g)
	if len(got) != 3 {
		t.Fatalf("cycle dropped nodes: got %v", got)
	}
	seen := map[string]bool{}
	for _, key := range got {
		seen[key.Name] = true
	}
	for _, name := range []string{"a", "b", "c"} {
		if !seen[name] {
			t.Errorf("missing %s in %v", name, got)
		}
	}
}

func TestReconcileOrder_IgnoresUnknownDeps(t *testing.T) {
	// b depends on a kustomization not in the graph — that edge is ignored.
	g := mkGraph(node("b", "b", "ghost"), node("a", "a"))
	got := ReconcileOrder(g)
	if len(got) != 2 {
		t.Fatalf("expected 2 nodes, got %v", got)
	}
}

func TestBuildRoots_DedupAndSort(t *testing.T) {
	g := mkGraph(
		node("one", "infra/base"),
		node("two", "apps/base"),
		node("three", "infra/base"), // duplicate path, collapses
		node("four", ""),            // empty path, dropped
	)
	got := BuildRoots(g)
	want := []string{"apps/base", "infra/base"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildRoots = %v, want %v", got, want)
	}
}

func TestBuildRoots_EmptyGraph(t *testing.T) {
	if got := BuildRoots(mkGraph()); len(got) != 0 {
		t.Fatalf("expected no roots, got %v", got)
	}
}

func TestCycleNodes_OnlyCycleMembers(t *testing.T) {
	// a<->b cycle; c and d are acyclic (d depends on c).
	g := mkGraph(node("a", "a", "b"), node("b", "b", "a"), node("c", "c"), node("d", "d", "c"))
	cycle := CycleNodes(g)
	if !cycle[k("a")] || !cycle[k("b")] {
		t.Errorf("expected a and b flagged as cycle nodes, got %v", cycle)
	}
	if cycle[k("c")] || cycle[k("d")] {
		t.Errorf("acyclic c/d must not be flagged, got %v", cycle)
	}
}

func TestCycleNodes_None(t *testing.T) {
	g := mkGraph(node("a", "a"), node("b", "b", "a"))
	if got := CycleNodes(g); len(got) != 0 {
		t.Fatalf("expected no cycle nodes, got %v", got)
	}
}

func TestDanglingDeps_ReferrerOnly(t *testing.T) {
	// b depends on a (present) and ghost (absent); a is clean.
	g := mkGraph(node("a", "a"), node("b", "b", "a", "ghost"))
	dangling := DanglingDeps(g)
	if _, ok := dangling[k("a")]; ok {
		t.Errorf("a has no dangling deps, should be absent: %v", dangling)
	}
	missing := dangling[k("b")]
	if len(missing) != 1 || missing[0] != k("ghost") {
		t.Errorf("expected b -> [ghost], got %v", missing)
	}
}
