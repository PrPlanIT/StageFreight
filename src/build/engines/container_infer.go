package engines

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The node convention mirrors go/rust: a reasonable default for the common case,
// config as the escape hatch for the rest — every case reachable, none privileged.
//
// DEFAULT (any node project): install → build, in a node image, output the built
// tree (dist/). Electron is a DETECTED specialization layered on top (a pack step,
// the wine image + .exe glob for a Windows target) — it's our niche, not node's
// norm, so it never becomes the baseline. Anything the default+detection don't fit
// uses the escape hatches (command/image/output/args); the engine runs any command
// in any image and captures any glob/file/directory, so all cases are capable.
// inferNodeBuild fills only the gaps config leaves; explicit config always wins.

// pkgJSON is the minimal slice of package.json the convention reads.
type pkgJSON struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
	DevDeps        map[string]string `json:"devDependencies"`
	Workspaces     json.RawMessage   `json:"workspaces"` // []string or {packages:[]} — npm/yarn
	Build          *struct {
		Directories struct {
			Output string `json:"output"`
		} `json:"directories"`
	} `json:"build"`
}

func (p *pkgJSON) hasWorkspaces() bool {
	s := strings.TrimSpace(string(p.Workspaces))
	return s != "" && s != "null" && s != "[]" && s != "{}"
}

// inferredBuild carries the convention's defaults for a node build.
type inferredBuild struct {
	Image   string
	Command string
	Output  string
}

// inferNodeBuild derives the default image/command/output for a node project at
// <rootDir>/<from>, for a given target OS. Deterministic + minimal — reads the
// lockfile + a small slice of package.json, no heuristics beyond the standard path.
func inferNodeBuild(rootDir, from, targetOS string) inferredBuild {
	pkgDir := filepath.Join(rootDir, from)
	pkg := readPkgJSON(filepath.Join(pkgDir, "package.json"))
	pm := detectPackageManager(rootDir, pkgDir, pkg)
	workspace := detectWorkspace(rootDir, pkgDir, pkg)
	electron := pkg != nil && pkg.DevDeps["electron"] != ""

	// install → build → (pack, electron only). A workspace builds recursively so
	// sibling packages a desktop app bundles (e.g. a web UI) are built first.
	install := pm + " install"
	if pm == "pnpm" {
		install = "pnpm install --no-frozen-lockfile"
	}
	build := pm + " run build"
	if workspace {
		build = workspaceBuildCommand(pm, pkg)
	}
	steps := []string{install, build}
	// `pack` is an electron packaging step, layered in only for a detected electron
	// app — a plain web/lib build is not force-packed.
	if electron && pkg != nil && pkg.Scripts["pack"] != "" {
		steps = append(steps, pm+" run pack")
	}

	// image: a node image by default; wine for a Windows electron target.
	image := "node:20"
	if electron && targetOS == "windows" {
		image = "electronuserland/builder:wine"
	}

	// output: electron produces an installer FILE in directories.output (Windows →
	// a .exe glob); a plain build produces a built TREE (dist/) captured as a
	// directory artifact that the archive walks.
	var output string
	if electron {
		outDir := "dist" // electron-builder default
		if pkg != nil && pkg.Build != nil && pkg.Build.Directories.Output != "" {
			outDir = pkg.Build.Directories.Output
		}
		glob := "*"
		if targetOS == "windows" {
			glob = "*.exe"
		}
		output = filepath.Join(from, outDir, glob)
	} else {
		output = filepath.Join(from, "dist")
	}

	return inferredBuild{
		Image:   image,
		Command: strings.Join(steps, " && "),
		Output:  filepath.ToSlash(output),
	}
}

// inferBuild dispatches to the per-language convention for a containerized builder.
// Adding a language is a case here plus its infer* function — the engine, docker
// run, artifact capture, and archiving are all shared.
func inferBuild(builder, rootDir, from, targetOS, targetArch string) inferredBuild {
	switch builder {
	case "elixir":
		return inferElixirBuild(rootDir, from)
	case "dotnet":
		return inferDotnetBuild(from, targetOS, targetArch)
	case "c":
		return inferCBuild(rootDir, from)
	case "python":
		return inferPythonBuild(rootDir, from)
	case "jvm":
		return inferJvmBuild(rootDir, from)
	default: // node
		return inferNodeBuild(rootDir, from, targetOS)
	}
}

// inferElixirBuild is the elixir convention: fetch prod deps, build assets for a
// Phoenix app, then `mix release` — producing a self-contained release tree under
// _build/prod/rel/, captured as a directory artifact. Runs in an elixir image.
// Escape hatch: a Phoenix app whose assets need node/yarn in the build sets image:
// to an elixir+node image (a plain elixir image suffices for esbuild/tailwind).
func inferElixirBuild(rootDir, from string) inferredBuild {
	steps := []string{"mix deps.get --only prod"}
	if isPhoenixApp(filepath.Join(rootDir, from)) {
		steps = append(steps, "MIX_ENV=prod mix assets.deploy")
	}
	steps = append(steps, "MIX_ENV=prod mix release")
	return inferredBuild{
		Image:   "elixir:1.17",
		Command: strings.Join(steps, " && "),
		Output:  filepath.ToSlash(filepath.Join(from, "_build/prod/rel/*")),
	}
}

// isPhoenixApp reports whether the mix project depends on phoenix — a text probe of
// mix.exs, no Elixir parsing needed.
func isPhoenixApp(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "mix.exs"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), ":phoenix")
}

// inferDotnetBuild is the .NET convention (C#/F#/VB): restore, then publish a
// self-contained Release build for the target runtime (RID) into publish/ — a
// directory artifact the archive walks. Runs in the .NET SDK image. Self-contained
// per-RID yields a standalone artifact like a go binary; drop --self-contained via
// the command escape hatch for a framework-dependent build.
func inferDotnetBuild(from, targetOS, targetArch string) inferredBuild {
	rid := dotnetRID(targetOS, targetArch)
	command := fmt.Sprintf("dotnet restore && dotnet publish -c Release -r %s --self-contained -o publish", rid)
	return inferredBuild{
		Image:   "mcr.microsoft.com/dotnet/sdk:8.0",
		Command: command,
		Output:  filepath.ToSlash(filepath.Join(from, "publish")),
	}
}

// dotnetRID maps a target OS/arch to a .NET runtime identifier (win-x64, linux-arm64,
// osx-arm64, …) — the .NET analogue of GOOS/GOARCH. Unknown parts pass through.
func dotnetRID(targetOS, targetArch string) string {
	osPart := map[string]string{"windows": "win", "linux": "linux", "darwin": "osx"}[targetOS]
	if osPart == "" {
		osPart = targetOS
	}
	archPart := map[string]string{"amd64": "x64", "386": "x86", "arm64": "arm64", "arm": "arm"}[targetArch]
	if archPart == "" {
		archPart = targetArch
	}
	return osPart + "-" + archPart
}

// inferCBuild is the C convention. Unlike go/rust/dotnet, C has no canonical build
// tool, so the convention DETECTS the build system and runs it, in a gcc image. It
// also has no canonical output: cmake/meson use a build/ dir (defaulted here), but a
// raw Makefile can put the artifact anywhere — so output is left empty for those and
// must be set via config (the engine errors clearly if it isn't). This is the
// "reasonable default" degrading gracefully to escape-hatch-the-output.
func inferCBuild(rootDir, from string) inferredBuild {
	dir := filepath.Join(rootDir, from)
	var command, output string
	switch {
	case fileExists(filepath.Join(dir, "CMakeLists.txt")):
		command = "cmake -B build && cmake --build build"
		output = filepath.Join(from, "build")
	case fileExists(filepath.Join(dir, "meson.build")):
		command = "meson setup build && ninja -C build"
		output = filepath.Join(from, "build")
	case fileExists(filepath.Join(dir, "configure")):
		command = "./configure && make" // artifact location varies — set output:
	default: // Makefile / makefile / bare tree
		command = "make" // artifact location varies — set output:
	}
	return inferredBuild{
		Image:   "gcc:13",
		Command: command,
		Output:  filepath.ToSlash(output),
	}
}

// inferPythonBuild is the Python convention. Python is interpreted — most apps ship
// as a docker image (kind: docker), not this builder — so builder: python targets
// the two things that ARE artifacts: a wheel/sdist (python -m build → dist/, the
// default) or a frozen standalone binary (a detected PyInstaller .spec → dist/).
func inferPythonBuild(rootDir, from string) inferredBuild {
	dir := filepath.Join(rootDir, from)
	if spec := findSpec(dir); spec != "" {
		return inferredBuild{
			Image:   "python:3.12",
			Command: "pip install pyinstaller && pyinstaller --distpath dist " + spec,
			Output:  filepath.ToSlash(filepath.Join(from, "dist")),
		}
	}
	return inferredBuild{
		Image:   "python:3.12",
		Command: "pip install build && python -m build --outdir dist",
		Output:  filepath.ToSlash(filepath.Join(from, "dist")),
	}
}

// findSpec returns the basename of a PyInstaller .spec file in dir, or "".
func findSpec(dir string) string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.spec"))
	if len(matches) > 0 {
		return filepath.Base(matches[0])
	}
	return ""
}

// inferJvmBuild is the JVM convention — one path for the whole family (Kotlin, Java,
// Scala, Groovy, Clojure), since they all build via Gradle or Maven to a jar. Maven
// (pom.xml) → mvn package → target/*.jar; otherwise Gradle → ./gradlew build (the
// wrapper if present, else the image's gradle) → build/libs/*.jar. A bare jar needs
// a JVM to run; a fat/shadow jar bundles deps (via the command escape hatch).
func inferJvmBuild(rootDir, from string) inferredBuild {
	dir := filepath.Join(rootDir, from)
	if fileExists(filepath.Join(dir, "pom.xml")) {
		return inferredBuild{
			Image:   "maven:3-eclipse-temurin-21",
			Command: "mvn -B package",
			Output:  filepath.ToSlash(filepath.Join(from, "target", "*.jar")),
		}
	}
	gradle := "gradle build"
	if fileExists(filepath.Join(dir, "gradlew")) {
		gradle = "./gradlew build"
	}
	return inferredBuild{
		Image:   "gradle:jdk21",
		Command: gradle,
		Output:  filepath.ToSlash(filepath.Join(from, "build", "libs", "*.jar")),
	}
}

// workspaceBuildCommand returns the recursive build for each package manager's
// workspace model (build every package, not just the entry one).
func workspaceBuildCommand(pm string, pkg *pkgJSON) string {
	switch pm {
	case "pnpm":
		return "pnpm -r build"
	case "npm":
		return "npm run build --workspaces --if-present"
	case "yarn":
		// Berry (>=2) uses foreach (workspace-tools); classic uses `workspaces run`.
		if yarnMajor(pkg) >= 2 {
			return "yarn workspaces foreach -A run build"
		}
		return "yarn workspaces run build"
	default:
		return pm + " run build"
	}
}

func yarnMajor(pkg *pkgJSON) int {
	if pkg == nil || pkg.PackageManager == "" {
		return 0
	}
	parts := strings.SplitN(pkg.PackageManager, "@", 2) // "yarn@3.6.4" → "3.6.4"
	if len(parts) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(strings.SplitN(parts[1], ".", 2)[0])
	return n
}

func readPkgJSON(path string) *pkgJSON {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p pkgJSON
	if json.Unmarshal(data, &p) != nil {
		return nil
	}
	return &p
}

// detectPackageManager prefers package.json's packageManager field, else a
// lockfile at the package dir or repo root; defaults to npm.
func detectPackageManager(rootDir, pkgDir string, pkg *pkgJSON) string {
	if pkg != nil && pkg.PackageManager != "" {
		return strings.SplitN(pkg.PackageManager, "@", 2)[0]
	}
	for _, dir := range []string{pkgDir, rootDir} {
		switch {
		case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
			return "pnpm"
		case fileExists(filepath.Join(dir, "yarn.lock")):
			return "yarn"
		case fileExists(filepath.Join(dir, "package-lock.json")):
			return "npm"
		}
	}
	return "npm"
}

// detectWorkspace covers all three managers: pnpm's pnpm-workspace.yaml, and the
// package.json "workspaces" field npm + yarn use (at the repo root or package dir).
func detectWorkspace(rootDir, pkgDir string, pkg *pkgJSON) bool {
	if fileExists(filepath.Join(rootDir, "pnpm-workspace.yaml")) {
		return true
	}
	if pkg != nil && pkg.hasWorkspaces() {
		return true
	}
	if root := readPkgJSON(filepath.Join(rootDir, "package.json")); root != nil && root.hasWorkspaces() {
		return true
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
