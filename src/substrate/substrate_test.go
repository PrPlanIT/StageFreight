package substrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func capSet(needs []Need) map[string]bool {
	m := map[string]bool{}
	for _, n := range needs {
		m[n.Capability] = true
	}
	return m
}

// Rust always needs a C toolchain (build scripts link); the crate graph adds the rest.
func TestInferRustNeeds_BaseAndCrateGraph(t *testing.T) {
	dir := t.TempDir()
	base := InferRustNeeds(dir)
	if len(base) != 1 || base[0].Capability != "c-toolchain" {
		t.Fatalf("no Cargo.lock → just c-toolchain, got %+v", base)
	}
	lock := "[[package]]\nname = \"aws-lc-sys\"\nversion = \"0.41.0\"\n[[package]]\nname = \"serde\"\n"
	if err := os.WriteFile(filepath.Join(dir, "Cargo.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	caps := capSet(InferRustNeeds(dir))
	for _, want := range []string{"c-toolchain", "cmake", "perl"} {
		if !caps[want] {
			t.Errorf("aws-lc-sys must imply %q; got %v", want, caps)
		}
	}
}

// Invariant: every capability the inference can emit MUST have a backend package
// mapping — otherwise an engine could emit a capability nothing can realize.
func TestEveryInferredCapabilityHasABackendMapping(t *testing.T) {
	if _, ok := capabilityPackages["c-toolchain"]; !ok {
		t.Error("c-toolchain must map to a package")
	}
	for crate, extras := range cargoNativeCrates {
		for _, n := range extras {
			if _, ok := capabilityPackages[n.Capability]; !ok {
				t.Errorf("crate %s emits capability %q with no apk mapping", crate, n.Capability)
			}
		}
	}
}

func TestInferRustNeeds_GitForBuildRs(t *testing.T) {
	dir := t.TempDir()
	if capSet(InferRustNeeds(dir))["git"] {
		t.Error("no build.rs → no git need")
	}
	if err := os.WriteFile(filepath.Join(dir, "build.rs"), []byte("fn main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !capSet(InferRustNeeds(dir))["git"] {
		t.Error("build.rs present → git need (version-stamping)")
	}
	if _, ok := capabilityPackages["git"]; !ok {
		t.Error("git capability must have a backend mapping")
	}
}

// A non-root runner reports a missing capability by name and never runs apk.
func TestApkRealizer_NonRootRunner(t *testing.T) {
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 1000 }

	r := &apkRealizer{}
	_, err := r.Realize(context.Background(), []Need{{Capability: "cmake", Reason: "native-crate", Source: "aws-lc-sys"}})
	if err == nil {
		t.Skip("cmake is already present in this image — nothing to install")
	}
	for _, want := range []string{"aws-lc-sys", "cmake", "uid 1000", "CI image"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestApkRealizer_PresentCapabilityNeedsNoInstall(t *testing.T) {
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return 1000 }

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "perl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	realized, err := (&apkRealizer{}).Realize(context.Background(), []Need{{Capability: "perl", Source: "test"}})
	if err != nil || len(realized) != 1 || !realized[0].Present {
		t.Fatalf("present capability: realized=%+v err=%v", realized, err)
	}
}
