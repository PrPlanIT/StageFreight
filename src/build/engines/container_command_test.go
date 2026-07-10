package engines

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestCommandEngine_PlanFromExplicitConfig confirms the binary-command engine does NO
// inference: it plans a ContainerMeta straight from the build's explicit image, command,
// and output (the escape hatch), and captures that output as the artifact.
func TestCommandEngine_PlanFromExplicitConfig(t *testing.T) {
	eng, err := build.GetV2(EngineCommand)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineCommand, err)
	}

	cfg := build.BuildConfig{
		ID:        "reference",
		Kind:      "command",
		Builder:   "command",
		Image:     "docker.io/library/golang:1.25",
		Command:   "go run ./... docs generate --output docs/generated",
		Output:    "docs/generated",
		Platforms: []build.Target{{OS: "linux", Arch: "amd64"}},
	}

	steps, err := eng.Plan(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(steps))
	}
	meta, ok := steps[0].Meta.(ContainerMeta)
	if !ok {
		t.Fatalf("step.Meta is %T, want ContainerMeta", steps[0].Meta)
	}
	if meta.Image != cfg.Image {
		t.Errorf("Image = %q, want %q (no inference)", meta.Image, cfg.Image)
	}
	if meta.Command != cfg.Command {
		t.Errorf("Command = %q, want %q (verbatim from config)", meta.Command, cfg.Command)
	}
	if meta.Artifact != "docs/generated" {
		t.Errorf("Artifact = %q, want docs/generated (the declared output)", meta.Artifact)
	}
}
