package engines

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

func TestNodeEnginePlan(t *testing.T) {
	e := &nodeEngine{}
	win := []build.Target{{OS: "windows", Arch: "amd64"}}

	// Required fields are enforced.
	for _, tc := range []struct {
		name string
		cfg  build.BuildConfig
	}{
		{"no image", build.BuildConfig{Command: "x", Output: "y", Platforms: win}},
		{"no command", build.BuildConfig{Image: "img", Output: "y", Platforms: win}},
		{"no output", build.BuildConfig{Image: "img", Command: "x", Platforms: win}},
	} {
		if _, err := e.Plan(context.Background(), tc.cfg); err == nil {
			t.Errorf("%s: expected a validation error", tc.name)
		}
	}

	// Valid config → one step per platform carrying ContainerMeta.
	cfg := build.BuildConfig{
		ID:        "desktop",
		Image:     "electronuserland/builder:wine",
		Command:   "pnpm install && pnpm pack",
		From:      "ui/desktop",
		Output:    "ui/desktop/release/*.exe",
		Env:       map[string]string{"CI": "1"},
		Platforms: win,
	}
	steps, err := e.Plan(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}
	s := steps[0]
	if s.Engine != EngineNode {
		t.Errorf("Engine = %q, want %q", s.Engine, EngineNode)
	}
	meta, ok := s.Meta.(ContainerMeta)
	if !ok {
		t.Fatalf("Meta is %T, want ContainerMeta", s.Meta)
	}
	if meta.Image != cfg.Image || meta.Command != cfg.Command || meta.WorkDir != cfg.From || meta.Artifact != cfg.Output {
		t.Errorf("meta mismatch: %+v", meta)
	}
	if meta.StepMetaKind() != "container" {
		t.Errorf("StepMetaKind = %q, want container", meta.StepMetaKind())
	}
}

// The engine is registered under the builder-node dispatch key, so a
// `builder: node` build (engineNameFor → "binary-node") resolves to it.
func TestNodeEngineRegistered(t *testing.T) {
	eng, err := build.GetV2(EngineNode)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineNode, err)
	}
	if eng.Name() != EngineNode {
		t.Errorf("Name = %q, want %q", eng.Name(), EngineNode)
	}
}
