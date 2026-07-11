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
