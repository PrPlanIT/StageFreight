package build

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestBuildOrder_StageIsADependencyEdge pins that a build's stage.from orders it AFTER the
// staged binary build — so a command build can invoke a freshly-built tool without an
// explicit depends_on (staging without the source built first is nonsensical).
func TestBuildOrder_StageIsADependencyEdge(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "reference", Kind: "command", Command: "./tool",
			Stage:   &config.StageConfig{From: "tool-bin", As: "tool"},
			Outputs: []config.OutputSpec{{Type: "tree", Source: "docs"}}},
		{ID: "tool-bin", Kind: "binary", Builder: "go", From: "./cmd/tool"},
	}
	ordered, err := BuildOrder(builds)
	if err != nil {
		t.Fatalf("BuildOrder: %v", err)
	}
	pos := map[string]int{}
	for i, b := range ordered {
		pos[b.ID] = i
	}
	if pos["tool-bin"] > pos["reference"] {
		t.Errorf("stage.from must order tool-bin before reference; got %v", func() []string {
			ids := make([]string, len(ordered))
			for i, b := range ordered {
				ids[i] = b.ID
			}
			return ids
		}())
	}
}

// TestBuildOrder_MultiDepsAndScribeRefsIgnored: depends_on is now a typed-ref list —
// multiple BUILD deps all order, and scribe: refs are not build edges (they don't
// appear as builds, so they must be ignored by the build graph).
func TestBuildOrder_MultiDepsAndScribeRefsIgnored(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "site", Kind: "command", Command: "mkdocs",
			DependsOn: config.StringOrList{"docgen", "compile", "scribe:ref-pages"}},
		{ID: "docgen", Kind: "command", Command: "gen"},
		{ID: "compile", Kind: "binary", Builder: "go", From: "./cmd"},
	}
	ordered, err := BuildOrder(builds)
	if err != nil {
		t.Fatalf("BuildOrder: %v (scribe: ref must not be treated as a build)", err)
	}
	pos := map[string]int{}
	for i, b := range ordered {
		pos[b.ID] = i
	}
	if pos["docgen"] > pos["site"] || pos["compile"] > pos["site"] {
		t.Errorf("both build deps must order before site; got %v", pos)
	}
}

// TestBuildOrder_UnknownBuildDep still errors (scribe: refs excluded from this check).
func TestBuildOrder_UnknownBuildDep(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "x", Kind: "command", Command: "c", DependsOn: config.StringOrList{"ghost"}},
	}
	if _, err := BuildOrder(builds); err == nil {
		t.Error("depends_on referencing an unknown build should error")
	}
}

// TestBuildOrder_StageUnknownFrom reports a stage.from that names no build.
func TestBuildOrder_StageUnknownFrom(t *testing.T) {
	builds := []config.BuildConfig{
		{ID: "reference", Kind: "command", Command: "./tool",
			Stage: &config.StageConfig{From: "nope", As: "tool"}},
	}
	if _, err := BuildOrder(builds); err == nil {
		t.Error("stage.from referencing an unknown build should error")
	}
}
