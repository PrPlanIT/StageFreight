package substrate

import (
	"os"
	"path/filepath"
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
