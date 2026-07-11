package engines

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

// TestCommandEngine_ExecuteStep_StagesAndExtracts is the DinD-safe-transport regression:
// the engine must docker cp the repo INTO the container (so the command sees repo files)
// and docker cp the produced tree back OUT onto the host — never a bind mount (which would
// resolve against a remote/DinD daemon's filesystem, not the job checkout). A MARKER file
// staged in proves cp-in; the extracted tree on the host proves cp-out.
func TestCommandEngine_ExecuteStep_StagesAndExtracts(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MARKER"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	eng, err := build.GetV2(EngineCommand)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineCommand, err)
	}
	cfg := build.BuildConfig{
		ID: "reference", Kind: "command", Builder: "command",
		Image:     "busybox",
		Command:   "test -f MARKER && mkdir -p out && echo hi > out/f.txt",
		Output:    "out",
		Platforms: []build.Target{{OS: "linux", Arch: "amd64"}},
	}
	steps, err := eng.Plan(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	res, err := eng.ExecuteStep(context.Background(), steps[0])
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].Type != "tree" {
		t.Fatalf("artifacts = %+v, want exactly one tree", res.Artifacts)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out", "f.txt"))
	if err != nil || string(got) != "hi\n" {
		t.Fatalf("tree not extracted to host <cwd>/out: got=%q err=%v", got, err)
	}
}

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
