package engines

import (
	"encoding/json"
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
