package substrate

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// InferRustNeeds derives the substrate capabilities a Rust build requires from the
// crate itself, NOT from operator config (discover, don't burden). Rust always needs
// a C toolchain — build scripts are themselves linked Rust executables, so even a
// pure-Rust crate fails with "linker cc not found". Specific crates pull additional
// native build deps, learned by scanning the resolved crate graph (Cargo.lock).
func InferRustNeeds(manifestDir string) []Need {
	needs := []Need{
		{Capability: "c-toolchain", Reason: "rust-build-script-linking", Source: "cargo"},
	}
	// A build.rs commonly shells out to git for version-stamping (directly or via a
	// script, e.g. version.sh → git rev-parse) — and the SF image is git-less. Provide
	// git when a build.rs is present so those scripts resolve. (Heuristic: not every
	// build.rs needs git, but it's cheap, cached, and covers the common case; a future
	// refinement could scan build.rs + referenced scripts.)
	if _, err := os.Stat(filepath.Join(manifestDir, "build.rs")); err == nil {
		needs = append(needs, Need{Capability: "git", Reason: "build-script-version-control", Source: "build.rs"})
	}
	data, err := os.ReadFile(filepath.Join(manifestDir, "Cargo.lock"))
	if err != nil {
		return needs // no lock graph to scan; base C toolchain still applies
	}
	present := cargoLockCrates(string(data))
	for crate, extra := range cargoNativeCrates {
		if present[crate] {
			needs = append(needs, extra...)
		}
	}
	return needs
}

// InferRustTestNeeds is InferRustNeeds plus the capabilities a crate's tests run: every
// program the crate's Rust sources execute by name (Command::new("ssh")) that maps to a
// capability. A test exercises the crate's own code as well as its test code, so the
// whole crate is scanned, not only tests/.
func InferRustTestNeeds(manifestDir string) []Need {
	needs := InferRustNeeds(manifestDir)
	have := map[string]bool{}
	for _, n := range needs {
		have[n.Capability] = true
	}
	for _, prog := range rustExecutedPrograms(manifestDir) {
		c := programCapability[prog]
		if c == "" || have[c] {
			continue
		}
		have[c] = true
		needs = append(needs, Need{Capability: c, Reason: "tests-exec-" + prog, Source: prog})
	}
	return needs
}

// programCapability maps a program a test executes to the capability that provides it.
// A program not listed is left to the image.
var programCapability = map[string]string{
	"git":        "git",
	"bash":       "bash",
	"ssh":        "openssh",
	"ssh-keygen": "openssh",
}

// rustCommandNew matches a program started by name: Command::new("prog"), with any path
// prefix (std::process::, tokio::process::).
var rustCommandNew = regexp.MustCompile(`Command::new\(\s*"([^"/\\]+)"\s*\)`)

// rustExecutedPrograms lists, sorted, the programs the .rs files under dir start by
// name. Hidden directories and cargo's target/ are skipped.
func rustExecutedPrograms(dir string) []string {
	seen := map[string]bool{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "target") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".rs") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range rustCommandNew.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// cargoNativeCrates maps a crate (when present in the graph) to the EXTRA
// capabilities its native build needs. Capabilities, never packages — the substrate
// backend resolves them per distro.
var cargoNativeCrates = map[string][]Need{
	"aws-lc-sys": {
		{Capability: "cmake", Reason: "crate-native-build", Source: "aws-lc-sys"},
		{Capability: "perl", Reason: "crate-native-build", Source: "aws-lc-sys"},
	},
	"ring": {
		{Capability: "c-toolchain", Reason: "crate-native-build", Source: "ring"},
	},
	"openssl-sys": {
		{Capability: "pkg-config", Reason: "crate-native-build", Source: "openssl-sys"},
		{Capability: "openssl", Reason: "crate-native-build", Source: "openssl-sys"},
	},
}

// cargoLockCrates extracts the set of crate names from a Cargo.lock (TOML
// `name = "..."` lines under [[package]]).
func cargoLockCrates(lock string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(lock, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `name = "`) {
			out[strings.TrimSuffix(strings.TrimPrefix(line, `name = "`), `"`)] = true
		}
	}
	return out
}
