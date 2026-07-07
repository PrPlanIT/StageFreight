package engines

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

var win = []build.Target{{OS: "windows", Arch: "amd64"}}

func TestNodeEnginePlan_RequiresFrom(t *testing.T) {
	if _, err := (&nodeEngine{}).Plan(context.Background(), build.BuildConfig{Platforms: win}); err == nil {
		t.Error("expected an error when from is missing")
	}
}

// Config is the escape hatch: explicit image/command/output override the
// convention rather than being required.
func TestNodeEnginePlan_ConfigOverrides(t *testing.T) {
	cfg := build.BuildConfig{
		ID: "desktop", From: "ui/desktop",
		Image: "myimage:latest", Command: "make windows", Output: "out/*.exe",
		Platforms: win,
	}
	steps, err := (&nodeEngine{}).Plan(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	meta := steps[0].Meta.(ContainerMeta)
	if meta.Image != "myimage:latest" || meta.Command != "make windows" || meta.Artifact != "out/*.exe" {
		t.Errorf("explicit config should override inference; got %+v", meta)
	}
}

// The convention: builder: node, from: <dir> infers the whole build for a pnpm
// electron app targeting Windows — wine image, install → recursive build → pack,
// and the electron-builder output dir as a .exe glob.
func TestInferNodeBuild_ElectronWindows(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pnpm-workspace.yaml"), "packages:\n  - ui/*\n")
	if err := os.MkdirAll(filepath.Join(dir, "ui/desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "ui/desktop/package.json"), `{
	  "packageManager": "pnpm@9",
	  "scripts": {"build": "vite build", "pack": "electron-builder --win"},
	  "devDependencies": {"electron": "31.0.0"},
	  "build": {"directories": {"output": "release"}}
	}`)

	inf := inferNodeBuild(dir, "ui/desktop", "windows")
	if inf.Image != "electronuserland/builder:wine" {
		t.Errorf("image = %q, want the wine image", inf.Image)
	}
	for _, want := range []string{"pnpm install", "pnpm -r build", "pnpm run pack"} {
		if !strings.Contains(inf.Command, want) {
			t.Errorf("command missing %q; got %q", want, inf.Command)
		}
	}
	if inf.Output != "ui/desktop/release/*.exe" {
		t.Errorf("output = %q, want ui/desktop/release/*.exe", inf.Output)
	}
}

func TestNodeEngineRegistered(t *testing.T) {
	eng, err := build.GetV2(EngineNode)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineNode, err)
	}
	if eng.Name() != EngineNode {
		t.Errorf("Name = %q, want %q", eng.Name(), EngineNode)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
