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
	// pnpm's persistent store rides the SF cache root as node/pnpm-store.
	if inf.CacheEnv != "PNPM_STORE_DIR" || len(inf.CacheSubdir) != 2 || inf.CacheSubdir[1] != "pnpm-store" {
		t.Errorf("pnpm cache spec = %v / %q, want [node pnpm-store] + PNPM_STORE_DIR", inf.CacheSubdir, inf.CacheEnv)
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
	// Frozen install by default: npm's frozen install is `npm ci`.
	if !strings.Contains(inf.Command, "npm ci") || !strings.Contains(inf.Command, "npm run build") {
		t.Errorf("command = %q, want npm ci + build", inf.Command)
	}
	if strings.Contains(inf.Command, "pack") {
		t.Errorf("a non-electron build must not pack; got %q", inf.Command)
	}
	if inf.Output != "dist" {
		t.Errorf("output = %q, want the dist tree", inf.Output)
	}
	if inf.CacheEnv != "npm_config_cache" || len(inf.CacheSubdir) != 2 || inf.CacheSubdir[1] != "npm-cache" {
		t.Errorf("npm cache spec = %v / %q, want [node npm-cache] + npm_config_cache", inf.CacheSubdir, inf.CacheEnv)
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
	if inf.CacheEnv != "YARN_CACHE_FOLDER" || len(inf.CacheSubdir) != 2 || inf.CacheSubdir[1] != "yarn-cache" {
		t.Errorf("yarn cache spec = %v / %q, want [node yarn-cache] + YARN_CACHE_FOLDER", inf.CacheSubdir, inf.CacheEnv)
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

// C has no canonical build tool: cmake is detected and gets a build/ output.
func TestInferCBuild_Cmake(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "CMakeLists.txt"), "project(app)\n")

	inf := inferBuild("c", dir, ".", "linux", "amd64")
	if inf.Image != "gcc:13" {
		t.Errorf("image = %q, want gcc:13", inf.Image)
	}
	if !strings.Contains(inf.Command, "cmake --build build") {
		t.Errorf("command = %q, want a cmake build", inf.Command)
	}
	if inf.Output != "build" {
		t.Errorf("output = %q, want build", inf.Output)
	}
}

// A raw Makefile has no inferable artifact location — output stays empty (the
// escape hatch), and the engine refuses to guess (must not capture the repo root).
func TestInferCBuild_MakefileHasNoOutput(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "Makefile"), "all:\n\tgcc -o app main.c\n")

	inf := inferBuild("c", dir, ".", "linux", "amd64")
	if inf.Command != "make" {
		t.Errorf("command = %q, want make", inf.Command)
	}
	if inf.Output != "" {
		t.Errorf("output = %q, want empty (unknowable)", inf.Output)
	}
}

func TestCBuild_RequiresOutputWhenUnknowable(t *testing.T) {
	e := &containerEngine{name: EngineC, builder: "c"}
	// cwd has no CMakeLists/meson under this from → make → no inferred output.
	if _, err := e.Plan(context.Background(), build.BuildConfig{ID: "app", From: "no-such-c-proj", Platforms: win}); err == nil {
		t.Error("expected an error when the C artifact location can't be inferred and output is unset")
	}
	// With output set, it plans fine.
	steps, err := e.Plan(context.Background(), build.BuildConfig{ID: "app", From: "no-such-c-proj", Output: "app", Platforms: win})
	if err != nil {
		t.Fatalf("Plan with output set: %v", err)
	}
	if steps[0].Meta.(ContainerMeta).Command != "make" {
		t.Errorf("command = %q, want make", steps[0].Meta.(ContainerMeta).Command)
	}
}

// Python: the default is a wheel/sdist build; a PyInstaller .spec is a detected
// variant (a frozen binary) — same detected-specialization pattern as electron.
func TestInferPythonBuild_Wheel(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pyproject.toml"), "[project]\nname = \"x\"\n")

	inf := inferBuild("python", dir, ".", "linux", "amd64")
	if inf.Image != "python:3.12" {
		t.Errorf("image = %q, want python:3.12", inf.Image)
	}
	if !strings.Contains(inf.Command, "python -m build") {
		t.Errorf("command = %q, want python -m build", inf.Command)
	}
	if inf.Output != "dist" {
		t.Errorf("output = %q, want dist", inf.Output)
	}
}

func TestInferPythonBuild_PyInstaller(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "app.spec"), "# pyinstaller spec\n")

	inf := inferBuild("python", dir, ".", "linux", "amd64")
	if !strings.Contains(inf.Command, "pyinstaller") || !strings.Contains(inf.Command, "app.spec") {
		t.Errorf("command = %q, want pyinstaller with the spec", inf.Command)
	}
}

func TestCAndPythonEnginesRegistered(t *testing.T) {
	for _, name := range []string{EngineC, EnginePython} {
		eng, err := build.GetV2(name)
		if err != nil {
			t.Fatalf("GetV2(%q): %v", name, err)
		}
		if eng.Name() != name {
			t.Errorf("Name = %q, want %q", eng.Name(), name)
		}
	}
}

// JVM is one convention for the whole family: a gradle project (wrapper present)
// builds via ./gradlew to build/libs/*.jar in a gradle image.
func TestInferJvmBuild_Gradle(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "build.gradle.kts"), "plugins {}\n")
	mustWrite(t, filepath.Join(dir, "gradlew"), "#!/bin/sh\n")

	inf := inferBuild("jvm", dir, ".", "linux", "amd64")
	if inf.Image != "gradle:jdk21" {
		t.Errorf("image = %q, want gradle:jdk21", inf.Image)
	}
	if inf.Command != "./gradlew build" {
		t.Errorf("command = %q, want ./gradlew build", inf.Command)
	}
	if inf.Output != "build/libs/*.jar" {
		t.Errorf("output = %q, want build/libs/*.jar", inf.Output)
	}
}

// A maven project (pom.xml) builds via mvn to target/*.jar — same builder.
func TestInferJvmBuild_Maven(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pom.xml"), "<project/>\n")

	inf := inferBuild("jvm", dir, ".", "linux", "amd64")
	if inf.Image != "maven:3-eclipse-temurin-21" {
		t.Errorf("image = %q, want the maven image", inf.Image)
	}
	if !strings.Contains(inf.Command, "mvn -B package") {
		t.Errorf("command = %q, want mvn package", inf.Command)
	}
	if inf.Output != "target/*.jar" {
		t.Errorf("output = %q, want target/*.jar", inf.Output)
	}
}

func TestJvmEngineRegistered(t *testing.T) {
	eng, err := build.GetV2(EngineJVM)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineJVM, err)
	}
	if eng.Name() != EngineJVM {
		t.Errorf("Name = %q, want %q", eng.Name(), EngineJVM)
	}
}

// Android: assemble a release APK in the SDK image, then apksigner-sign it with a
// keystore supplied as CI-secret env vars that SF forwards into the container.
func TestInferAndroidBuild(t *testing.T) {
	inf := inferBuild("android", "", ".", "linux", "arm64")
	if inf.Image != "ghcr.io/cirruslabs/android-sdk:34" {
		t.Errorf("image = %q, want the Android SDK image", inf.Image)
	}
	for _, want := range []string{"./gradlew assembleRelease", "apksigner sign", "base64 -d"} {
		if !strings.Contains(inf.Command, want) {
			t.Errorf("command missing %q; got %q", want, inf.Command)
		}
	}
	if inf.Output != "app/build/outputs/apk/release/*.apk" {
		t.Errorf("output = %q, want the release apk glob", inf.Output)
	}
	want := []string{"ANDROID_KEYSTORE_BASE64", "ANDROID_KEYSTORE_PASSWORD", "ANDROID_KEY_ALIAS", "ANDROID_KEY_PASSWORD"}
	if strings.Join(inf.ForwardEnv, ",") != strings.Join(want, ",") {
		t.Errorf("ForwardEnv = %v, want %v", inf.ForwardEnv, want)
	}
}

// Plan threads ForwardEnv into the step meta so ExecuteStep forwards the secrets.
func TestAndroidPlanForwardsSecrets(t *testing.T) {
	steps, err := (&containerEngine{name: EngineAndroid, builder: "android"}).Plan(
		context.Background(),
		build.BuildConfig{ID: "app", From: ".", Platforms: []build.Target{{OS: "linux", Arch: "arm64"}}},
	)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	meta := steps[0].Meta.(ContainerMeta)
	if len(meta.ForwardEnv) == 0 || meta.ForwardEnv[0] != "ANDROID_KEYSTORE_BASE64" {
		t.Errorf("ForwardEnv not threaded into meta: %v", meta.ForwardEnv)
	}
}

func TestAndroidEngineRegistered(t *testing.T) {
	eng, err := build.GetV2(EngineAndroid)
	if err != nil {
		t.Fatalf("GetV2(%q): %v", EngineAndroid, err)
	}
	if eng.Name() != EngineAndroid {
		t.Errorf("Name = %q, want %q", eng.Name(), EngineAndroid)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
