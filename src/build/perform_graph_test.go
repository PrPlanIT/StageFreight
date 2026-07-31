package build

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func nodeIDs(nodes []PerformNode) []string {
	var ids []string
	for _, n := range nodes {
		if n.IsScribe() {
			ids = append(ids, "scribe:"+n.ScribeID)
		} else {
			ids = append(ids, n.Build.ID)
		}
	}
	return ids
}

func posOf(ids []string) map[string]int {
	m := map[string]int{}
	for i, id := range ids {
		m[id] = i
	}
	return m
}

// THE doc-site chain: docgen(build) → ref-pages(scribe, build:docgen) → site;
// inventory(scribe, early) → site. Order must satisfy every edge, and a late
// (non-build-fed) scribe item must NOT appear in the perform order.
func TestPerformOrder_DocSiteChain(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "docgen", Kind: "command", Command: "gen"},
		{ID: "site", Kind: "command", Command: "mkdocs",
			DependsOn: config.StringOrList{"scribe:ref-pages", "scribe:inventory"}},
	}
	stencils := config.OrderedStencils{
		{ID: "ref-pages", Type: "include", Build: "docgen", Path: "docs/cli.md"},
		{ID: "inventory", Type: "k8s-inventory"},
		{ID: "pulls", Message: "{docker.pulls}"}, // late — not consumed by any build
	}

	nodes, err := PerformOrder(builds, stencils)
	if err != nil {
		t.Fatal(err)
	}
	ids := nodeIDs(nodes)
	pos := posOf(ids)

	for _, want := range []string{"docgen", "scribe:ref-pages", "scribe:inventory", "site"} {
		if _, ok := pos[want]; !ok {
			t.Fatalf("%q missing from perform order %v", want, ids)
		}
	}
	if pos["docgen"] > pos["scribe:ref-pages"] {
		t.Errorf("docgen must precede its consumer ref-pages; got %v", ids)
	}
	if pos["scribe:ref-pages"] > pos["site"] || pos["scribe:inventory"] > pos["site"] {
		t.Errorf("both scribe inputs must precede the site build; got %v", ids)
	}
	if _, present := pos["scribe:pulls"]; present {
		t.Errorf("a late (non-build-fed) scribe item must not be in the perform order; got %v", ids)
	}
}

// ScribeRenderSlots assigns each scribe node to the build right before it: the
// doc-site chain puts both composes after `reference` (and before `site`), and an
// item before any build lands in `first`.
func TestScribeRenderSlots(t *testing.T) {
	bDocgen := &config.BuildConfig{ID: "docgen"}
	bSite := &config.BuildConfig{ID: "site"}
	order := []PerformNode{
		{ScribeID: "early"}, // before any build
		{Build: bDocgen},
		{ScribeID: "ref-pages"},
		{ScribeID: "inventory"},
		{Build: bSite},
	}
	first, after := ScribeRenderSlots(order)

	if len(first) != 1 || first[0] != "early" {
		t.Fatalf("first = %v, want [early]", first)
	}
	got := after["docgen"]
	if len(got) != 2 || got[0] != "ref-pages" || got[1] != "inventory" {
		t.Fatalf("after[docgen] = %v, want [ref-pages inventory]", got)
	}
	if len(after["site"]) != 0 {
		t.Fatalf("nothing should render after site, got %v", after["site"])
	}
}

// No build consumes scribe → the order is just the builds (scribe stays late).
func TestPerformOrder_NoBuildFedScribe(t *testing.T) {
	builds := []config.BuildConfig{{ID: "img", Kind: "docker"}}
	stencils := config.OrderedStencils{{ID: "license"}}
	nodes, err := PerformOrder(builds, stencils)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].IsScribe() || nodes[0].Build.ID != "img" {
		t.Fatalf("expected just the build, got %v", nodeIDs(nodes))
	}
}

// A cycle (build depends on a scribe item whose upstream build depends back) errors.
func TestPerformOrder_Cycle(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "a", Kind: "command", Command: "c", DependsOn: config.StringOrList{"scribe:s"}},
	}
	stencils := config.OrderedStencils{
		{ID: "s", Type: "include", Build: "a"}, // s ← a, but a ← s  ⇒ cycle
	}
	if _, err := PerformOrder(builds, stencils); err == nil {
		t.Error("a build↔scribe cycle should error")
	}
}
