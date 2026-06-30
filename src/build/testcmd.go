package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultTestCommand returns the native test invocation for a builder plus the
// working directory it must run in. It owns "what does testing a Go/Rust project
// mean" so the test subsystem never duplicates toolchain knowledge.
//
//	go   → go test ./...      (from the module root — the dir holding go.mod)
//	rust → cargo test         (single crate)
//	       cargo test --workspace  (when the crate root's Cargo.toml is a [workspace])
//
// fromDir is the build's `From` (the main-package / crate dir), which may sit
// BELOW the module/crate root — stagefreight builds `./src/cli` but its go.mod is
// at the repo root; dd-ui's go.mod is in `api/`. So the working dir is resolved by
// walking up from fromDir to the nearest manifest, bounded by rootDir.
//
// Only the BASE command is returned; suite flags (-race, -tags, --features, …) and
// the synthesized-default policy are layered by the caller.
func DefaultTestCommand(builder, fromDir, rootDir string) (args []string, workdir string, err error) {
	start := fromDir
	if start == "" {
		start = rootDir
	}
	switch builder {
	case "go", "":
		dir := findManifestRoot(start, rootDir, "go.mod")
		if dir == "" {
			dir = rootDir
		}
		return []string{"go", "test", "./..."}, dir, nil
	case "rust":
		dir := findManifestRoot(start, rootDir, "Cargo.toml")
		if dir == "" {
			dir = rootDir
		}
		cmd := []string{"cargo", "test"}
		if isCargoWorkspace(filepath.Join(dir, "Cargo.toml")) {
			cmd = append(cmd, "--workspace")
		}
		return cmd, dir, nil
	default:
		return nil, start, fmt.Errorf("no default test command for builder %q (supported: go, rust)", builder)
	}
}

// findManifestRoot walks up from start looking for a directory containing
// manifest (go.mod / Cargo.toml), stopping at (and not above) rootDir. Returns ""
// if none is found within the bound.
func findManifestRoot(start, rootDir, manifest string) string {
	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		}
		return filepath.Clean(filepath.Join(rootDir, p))
	}
	cur := abs(start)
	root := filepath.Clean(rootDir)
	for {
		if _, err := os.Stat(filepath.Join(cur, manifest)); err == nil {
			return cur
		}
		if cur == root || cur == filepath.Dir(cur) {
			return ""
		}
		parent := filepath.Dir(cur)
		// Do not climb above rootDir.
		if !strings.HasPrefix(parent, root) {
			return ""
		}
		cur = parent
	}
}

// isCargoWorkspace reports whether a Cargo.toml declares a [workspace] table.
func isCargoWorkspace(cargoToml string) bool {
	data, err := os.ReadFile(cargoToml)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "[workspace]" {
			return true
		}
	}
	return false
}
