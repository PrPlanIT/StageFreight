package engines

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// The Go engine plans behavior-preserving steps: registered under "binary-go" (the
// dispatch key), one step per target, with the SAME output path and step ID the
// pre-seam Go path produced for a libc-less target. This locks "byte-identical Go
// output" across the seam refactor at the planning layer.
func TestBinaryEngine_PlanIsBehaviorPreserving(t *testing.T) {
	eng, err := build.GetV2("binary-go")
	if err != nil {
		t.Fatalf("the Go engine must register under binary-go: %v", err)
	}
	if eng.Name() != "binary-go" {
		t.Errorf("engine name: %q", eng.Name())
	}

	steps, err := eng.Plan(context.Background(), build.BuildConfig{
		ID: "myapp", Kind: "binary", Builder: "go", From: "./cmd/myapp",
		Platforms: []build.Target{{OS: "linux", Arch: "amd64"}, {OS: "windows", Arch: "amd64"}},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected one step per target, got %d", len(steps))
	}

	linux := steps[0]
	if linux.Engine != "binary-go" {
		t.Errorf("step.Engine must be the dispatch key, got %q", linux.Engine)
	}
	if linux.Target.OS != "linux" || linux.Target.Arch != "amd64" {
		t.Errorf("target: %+v", linux.Target)
	}
	if linux.StepID != "myapp--linux-amd64" {
		t.Errorf("step ID changed: %q", linux.StepID)
	}
	if got := linux.Outputs[0].Path; got != build.DistDir+"/linux-amd64/myapp" {
		t.Errorf("output path changed: %q", got)
	}

	// Windows still gets the .exe suffix.
	if got := steps[1].Outputs[0].Path; got != build.DistDir+"/windows-amd64/myapp.exe" {
		t.Errorf("windows output path: %q", got)
	}
}

// A non-go builder is rejected by the Go engine (the contributor dispatches the right
// engine; a mismatch is a hard error, never a silent wrong-language build).
func TestBinaryEngine_RejectsNonGoBuilder(t *testing.T) {
	eng, _ := build.GetV2("binary-go")
	if _, err := eng.Plan(context.Background(), build.BuildConfig{
		ID: "x", Builder: "rust", From: "./src/main.rs",
		Platforms: []build.Target{{OS: "linux", Arch: "amd64"}},
	}); err == nil {
		t.Fatal("the Go engine must reject a non-go builder")
	}
}
