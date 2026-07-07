package engines

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// The node convention mirrors go/rust: a reasonable default for the common case,
// config as the escape hatch for the rest — every case reachable, none privileged.
//
// DEFAULT (any node project): install → build, in a node image, output under dist/.
// Electron is a DETECTED specialization layered on top (a pack step, the wine image
// + .exe glob for a Windows target) — it's our niche, not node's norm, so it never
// becomes the baseline. Anything the default+detection don't fit uses the escape
// hatches (command/image/output/args); the engine runs any command in any image and
// captures any glob, so all cases are capable. inferNodeBuild fills only the gaps
// config leaves; explicit config always wins.

// pkgJSON is the minimal slice of package.json the convention reads.
type pkgJSON struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
	DevDeps        map[string]string `json:"devDependencies"`
	Build          *struct {
		Directories struct {
			Output string `json:"output"`
		} `json:"directories"`
	} `json:"build"`
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
	pkg := readPkgJSON(filepath.Join(rootDir, from, "package.json"))
	pm := detectPackageManager(rootDir, filepath.Join(rootDir, from), pkg)
	workspace := pm == "pnpm" && fileExists(filepath.Join(rootDir, "pnpm-workspace.yaml"))
	electron := pkg != nil && pkg.DevDeps["electron"] != ""

	// install → build → (pack). Workspace build is recursive so sibling packages a
	// desktop app bundles (e.g. a web UI) are built before it's packed.
	install := pm + " install"
	if pm == "pnpm" {
		install = "pnpm install --no-frozen-lockfile"
	}
	build := "pnpm run build"
	if workspace {
		build = "pnpm -r build"
	} else if pm != "pnpm" {
		build = pm + " run build"
	}
	// Default stops at build. `pack` is an electron packaging step, layered in only
	// for a detected electron app — a plain web/lib build is not force-packed.
	steps := []string{install, build}
	if electron && pkg != nil && pkg.Scripts["pack"] != "" {
		steps = append(steps, pm+" run pack")
	}

	// image: the one container-specific field. A Windows electron build needs wine;
	// otherwise a plain node image. Overridable via config.
	image := "node:20"
	if electron && targetOS == "windows" {
		image = "electronuserland/builder:wine"
	}

	// output: electron writes to its configured directories.output (default "dist");
	// a plain build to dist/. Windows electron installers are .exe.
	outDir := "dist"
	if pkg != nil && pkg.Build != nil && pkg.Build.Directories.Output != "" {
		outDir = pkg.Build.Directories.Output
	}
	glob := "*"
	if electron && targetOS == "windows" {
		glob = "*.exe"
	}

	return inferredBuild{
		Image:   image,
		Command: strings.Join(steps, " && "),
		Output:  filepath.ToSlash(filepath.Join(from, outDir, glob)),
	}
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
