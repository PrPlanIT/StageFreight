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
	if _, err := (&containerEngine{name: EngineNode, builder: "node"}).Plan(context.Background(), build.BuildConfig{Platforms: win}); err == nil {
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
	steps, err := (&containerEngine{name: EngineNode, builder: "node"}).Plan(context.Background(), cfg)
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

// The reasonable default (no electron): a plain npm web build gets the node image,
// install → build, dist output — and is NOT force-packed. Electron specialization
// stays absent; it's a detected variant, not the baseline.
func TestInferNodeBuild_PlainWeb(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "package-lock.json"), "{}")
	mustWrite(t, filepath.Join(dir, "package.json"), `{
	  "scripts": {"build": "vite build", "pack": "some-packer"}
	}`)

	inf := inferNodeBuild(dir, ".", "linux")
	if inf.Image != "node:20" {
		t.Errorf("image = %q, want node:20", inf.Image)
	}
	if !strings.Contains(inf.Command, "npm install") || !strings.Contains(inf.Command, "npm run build") {
		t.Errorf("command = %q, want install + build", inf.Command)
	}
	if strings.Contains(inf.Command, "pack") {
		t.Errorf("a non-electron build must not pack; got %q", inf.Command)
	}
	if inf.Output != "dist" {
		t.Errorf("output = %q, want the dist tree", inf.Output)
	}
}

// Workspace detection isn't pnpm-only: a yarn (berry) workspace gets yarn install
// + a recursive workspace build, no electron, dist tree output.
func TestInferNodeBuild_YarnWorkspace(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "yarn.lock"), "")
	mustWrite(t, filepath.Join(dir, "package.json"), `{
	  "packageManager": "yarn@3.6.0",
	  "workspaces": ["packages/*"],
	  "scripts": {"build": "tsc"}
	}`)

	inf := inferNodeBuild(dir, ".", "linux")
	if !strings.Contains(inf.Command, "yarn install") {
		t.Errorf("command = %q, want yarn install", inf.Command)
	}
	if !strings.Contains(inf.Command, "yarn workspaces foreach") {
		t.Errorf("workspace build should be recursive; got %q", inf.Command)
	}
	if inf.Image != "node:20" {
		t.Errorf("image = %q, want node:20", inf.Image)
	}
	if inf.Output != "dist" {
		t.Errorf("output = %q, want the dist tree", inf.Output)
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

// Elixir rides the same engine: builder: elixir, from: <dir> infers deps → assets
// (Phoenix) → mix release in an elixir image, capturing the release tree.
func TestInferElixirBuild_Phoenix(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "mix.exs"), `defmodule App.MixProject do
	  defp deps, do: [{:phoenix, "~> 1.7"}]
	end`)

	inf := inferBuild("elixir", dir, ".", "linux", "amd64")
	if inf.Image != "elixir:1.17" {
		t.Errorf("image = %q, want elixir:1.17", inf.Image)
	}
	for _, want := range []string{"mix deps.get", "mix assets.deploy", "mix release"} {
		if !strings.Contains(inf.Command, want) {
			t.Errorf("command missing %q; got %q", want, inf.Command)
		}
	}
	if inf.Output != "_build/prod/rel/*" {
		t.Errorf("output = %q, want the release tree", inf.Output)
	}
}

// A non-Phoenix elixir project skips the assets step (the detected specialization).
func TestInferElixirBuild_PlainLib(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "mix.exs"), `defmodule Lib.MixProject do
	  defp deps, do: [{:jason, "~> 1.4"}]
	end`)

	inf := inferBuild("elixir", dir, ".", "linux", "amd64")
	if strings.Contains(inf.Command, "assets.deploy") {
		t.Errorf("a non-phoenix app must not build assets; got %q", inf.Command)
	}
	if !strings.Contains(inf.Command, "mix release") {
		t.Errorf("command missing mix release; got %q", inf.Command)
	}
}

func TestElixirEngineRegistered(t *testing.T) {
	eng, err := build.GetV2(EngineElixir)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineElixir, err)
	}
	if eng.Name() != EngineElixir {
		t.Errorf("Name = %q, want %q", eng.Name(), EngineElixir)
	}
}

// .NET rides the same engine: builder: dotnet, from: <dir> infers restore →
// self-contained publish for the target RID in the .NET SDK image, capturing the
// publish tree. RID is the .NET analogue of GOOS/GOARCH.
func TestInferDotnetBuild_Windows(t *testing.T) {
	inf := inferBuild("dotnet", "", "src/App", "windows", "amd64")
	if inf.Image != "mcr.microsoft.com/dotnet/sdk:8.0" {
		t.Errorf("image = %q, want the .NET SDK image", inf.Image)
	}
	for _, want := range []string{"dotnet restore", "dotnet publish -c Release", "-r win-x64", "--self-contained"} {
		if !strings.Contains(inf.Command, want) {
			t.Errorf("command missing %q; got %q", want, inf.Command)
		}
	}
	if inf.Output != "src/App/publish" {
		t.Errorf("output = %q, want src/App/publish", inf.Output)
	}
}

func TestDotnetRID(t *testing.T) {
	cases := []struct{ os, arch, want string }{
		{"windows", "amd64", "win-x64"},
		{"linux", "amd64", "linux-x64"},
		{"linux", "arm64", "linux-arm64"},
		{"darwin", "arm64", "osx-arm64"},
	}
	for _, c := range cases {
		if got := dotnetRID(c.os, c.arch); got != c.want {
			t.Errorf("dotnetRID(%q,%q) = %q, want %q", c.os, c.arch, got, c.want)
		}
	}
}

func TestDotnetEngineRegistered(t *testing.T) {
	eng, err := build.GetV2(EngineDotnet)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineDotnet, err)
	}
	if eng.Name() != EngineDotnet {
		t.Errorf("Name = %q, want %q", eng.Name(), EngineDotnet)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
