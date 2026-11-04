package substrate

import (
	"context"
	"os"
	"os/exec"
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

// fakeApk stands in for apk: it records each call and, for an add, creates the
// probed binaries under --root — each printing its LD_LIBRARY_PATH — so a later
// realize sees them installed.
func fakeApk(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	old := apkCommand
	t.Cleanup(func() { apkCommand = old })
	apkCommand = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		root := ""
		for i, a := range args {
			if a == "--root" && i+1 < len(args) {
				root = args[i+1]
			}
		}
		if len(args) > 0 && args[0] == "add" && root != "" {
			for _, a := range args {
				for capability, pkgs := range capabilityPackages {
					if len(pkgs) == 1 && pkgs[0] == a && capabilityProbe[capability] != "" {
						bin := filepath.Join(root, "usr", "bin", capabilityProbe[capability])
						_ = os.MkdirAll(filepath.Dir(bin), 0o755)
						_ = os.WriteFile(bin, []byte("#!/bin/sh\necho \"$LD_LIBRARY_PATH\"\n"), 0o755)
					}
				}
			}
		}
		return nil, nil
	}
	return &calls
}

// emptyPath leaves PATH holding only an empty directory, so no host tool is present.
func emptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// asUID makes the realizer see uid as its effective uid.
func asUID(t *testing.T, uid int) {
	t.Helper()
	old := geteuid
	t.Cleanup(func() { geteuid = old })
	geteuid = func() int { return uid }
}

// onAlpine makes the realizer see release as the host's Alpine release.
func onAlpine(t *testing.T, release string) {
	t.Helper()
	old := alpineRelease
	t.Cleanup(func() { alpineRelease = old })
	alpineRelease = func() string { return release }
}

// registerFake registers fn for capability for the duration of the test.
func registerFake(t *testing.T, capability string, fn CapabilityRealizer) {
	t.Helper()
	capabilityRealizers.Lock()
	old, had := capabilityRealizers.m[capability]
	capabilityRealizers.Unlock()
	t.Cleanup(func() {
		capabilityRealizers.Lock()
		if had {
			capabilityRealizers.m[capability] = old
		} else {
			delete(capabilityRealizers.m, capability)
		}
		capabilityRealizers.Unlock()
	})
	RegisterCapability(capability, fn)
}

// A relocatable capability installs rootless into the substrate root — as any uid,
// without package scripts — and runs through a launcher that scopes the root's
// libraries to its own process; a second realize finds it.
func TestRealize_RelocatableIsRootless(t *testing.T) {
	calls := fakeApk(t)
	emptyPath(t)
	asUID(t, 4242)
	onAlpine(t, "3.24.0")
	cache := t.TempDir()
	root := filepath.Join(cache, "root", "3.24.0")
	r := &realizer{cacheDir: cache, apk: true}

	got, err := r.Realize(context.Background(), []Need{{Capability: "git", Source: "go test"}})
	if err != nil || len(got) != 1 {
		t.Fatalf("realize git: %+v %v", got, err)
	}
	add := strings.Join((*calls)[0], " ")
	for _, want := range []string{"--usermode", "--no-scripts", "--root " + root, "--initdb", "--keys-dir /etc/apk/keys"} {
		if !strings.Contains(add, want) {
			t.Errorf("apk call %q missing %q", add, want)
		}
	}
	env := ApplyEnv([]string{"PATH=/bin"}, got)
	if p, _ := lookupVar(env, "PATH"); p != filepath.Join(root, "wrap")+":/bin" {
		t.Errorf("PATH = %q, want the launchers first", p)
	}
	if v, _ := lookupVar(env, "GIT_EXEC_PATH"); v != filepath.Join(root, "usr", "libexec", "git-core") {
		t.Errorf("GIT_EXEC_PATH = %q", v)
	}
	if v, ok := lookupVar(env, "LD_LIBRARY_PATH"); ok {
		t.Errorf("LD_LIBRARY_PATH = %q leaked into the build env", v)
	}
	out, err := exec.Command(filepath.Join(root, "wrap", "git")).Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), root+"/wrap/../usr/lib:") {
		t.Fatalf("launcher: %q %v, want the root's libraries first", out, err)
	}

	*calls = nil
	again, err := r.Realize(context.Background(), []Need{{Capability: "git", Source: "go test"}})
	if err != nil || len(again) != 1 || !again[0].Present || len(*calls) != 0 {
		t.Fatalf("second realize must reuse the installed root without apk: %+v %v calls=%v", again, err, *calls)
	}
}

// A substrate root is per Alpine release: a host on a new release installs afresh
// instead of running binaries linked against another musl.
func TestRealize_RootIsPerAlpineRelease(t *testing.T) {
	calls := fakeApk(t)
	emptyPath(t)
	asUID(t, 4242)
	cache := t.TempDir()
	r := &realizer{cacheDir: cache, apk: true}
	need := []Need{{Capability: "git"}}

	onAlpine(t, "3.23.0")
	if _, err := r.Realize(context.Background(), need); err != nil {
		t.Fatal(err)
	}
	onAlpine(t, "3.24.0")
	*calls = nil
	got, err := r.Realize(context.Background(), need)
	if err != nil || len(*calls) == 0 || got[0].Present {
		t.Fatalf("new release must install: %+v %v calls=%v", got, err, *calls)
	}
	if add := strings.Join((*calls)[0], " "); !strings.Contains(add, filepath.Join(cache, "root", "3.24.0")) {
		t.Errorf("apk call %q, want the 3.24.0 root", add)
	}
}

// Launchers find the root from their own path, so a cache mounted elsewhere still runs.
func TestWriteRootWrappers_PathIndependent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a")
	bin := filepath.Join(root, "bin", "bash")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$LD_LIBRARY_PATH\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRootWrappers(root); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(root), "b")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(moved, "wrap", "bash"))
	cmd.Env = []string{"LD_LIBRARY_PATH=/keep"}
	out, err := cmd.Output()
	want := moved + "/wrap/../usr/lib:" + moved + "/wrap/../lib:/keep"
	if err != nil || strings.TrimSpace(string(out)) != want {
		t.Fatalf("moved launcher: %q %v, want %q", out, err, want)
	}
}

// A capability that only runs from /usr installs into the system as root; any other
// uid with no registered realizer gets an error naming what is missing.
func TestRealize_SystemCapabilityNeedsRoot(t *testing.T) {
	calls := fakeApk(t)
	emptyPath(t)
	r := &realizer{cacheDir: t.TempDir(), apk: true}
	need := []Need{{Capability: "cmake", Reason: "native-crate", Source: "aws-lc-sys"}}

	asUID(t, 4242)
	_, err := r.Realize(context.Background(), need)
	if err == nil || len(*calls) != 0 {
		t.Fatalf("non-root: err=%v calls=%v, want a refusal and no apk", err, *calls)
	}
	for _, want := range []string{"aws-lc-sys", "cmake", "uid 4242"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}

	asUID(t, 0)
	if _, err := r.Realize(context.Background(), need); err != nil || len(*calls) == 0 || (*calls)[0][0] != "add" {
		t.Fatalf("root: err=%v calls=%v, want apk add", err, *calls)
	}
}

// A registered realizer serves a system capability where the package can't be
// installed — non-root, or no apk — and steps aside as root with apk.
func TestRealize_RegisteredCapability(t *testing.T) {
	calls := fakeApk(t)
	emptyPath(t)
	registerFake(t, "c-toolchain", func(_ context.Context, _ string, n Need) (Realized, error) {
		return Realized{Need: n, Backend: "fake", Env: map[string]string{"CC": "cc"}}, nil
	})
	need := []Need{{Capability: "c-toolchain"}}

	asUID(t, 4242)
	got, err := (&realizer{apk: true}).Realize(context.Background(), need)
	if err != nil || len(got) != 1 || got[0].Backend != "fake" || len(*calls) != 0 {
		t.Fatalf("non-root: got %+v err=%v calls=%v", got, err, *calls)
	}
	got, err = (&realizer{apk: false}).Realize(context.Background(), need)
	if err != nil || len(got) != 1 || got[0].Backend != "fake" {
		t.Fatalf("no apk: got %+v err=%v", got, err)
	}

	asUID(t, 0)
	got, err = (&realizer{apk: true}).Realize(context.Background(), need)
	if err != nil || len(got) != 1 || got[0].Backend != "apk-alpine" || len(*calls) == 0 {
		t.Fatalf("root with apk: got %+v err=%v calls=%v, want build-base", got, err, *calls)
	}
}

// A registered realizer with nothing for this host falls through: to the refusal
// where apk exists, to the ambient environment where it doesn't.
func TestRealize_RegisteredUnavailableFallsThrough(t *testing.T) {
	calls := fakeApk(t)
	emptyPath(t)
	asUID(t, 4242)
	registerFake(t, "c-toolchain", func(_ context.Context, _ string, n Need) (Realized, error) {
		return Realized{}, ErrUnavailable
	})
	need := []Need{{Capability: "c-toolchain", Source: "go test -race"}}

	if _, err := (&realizer{apk: true}).Realize(context.Background(), need); err == nil || !strings.Contains(err.Error(), "uid 4242") {
		t.Fatalf("apk host: err=%v, want the non-root refusal", err)
	}
	if got, err := (&realizer{apk: false}).Realize(context.Background(), need); err != nil || len(got) != 0 || len(*calls) != 0 {
		t.Fatalf("no apk: got %+v err=%v, want the ambient environment trusted", got, err)
	}
}

// A tool already on PATH is used as is, on any runner.
func TestRealize_PresentCapabilityNeedsNoInstall(t *testing.T) {
	calls := fakeApk(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "perl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	realized, err := (&realizer{apk: true}).Realize(context.Background(), []Need{{Capability: "perl", Source: "test"}})
	if err != nil || len(realized) != 1 || !realized[0].Present || len(*calls) != 0 {
		t.Fatalf("present capability: realized=%+v err=%v calls=%v", realized, err, *calls)
	}
}

// ApplyEnv prepends every capability's dirs once, keeps the base's own entries, sets
// vars, and leaves its input untouched.
func TestApplyEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/h"}
	got := ApplyEnv(base, []Realized{
		{PathDirs: []string{"/a/bin"}},
		{PathDirs: []string{"/z/wrappers", "/a/bin"}, Env: map[string]string{"CC": "cc"}},
	})
	want := map[string]string{"PATH": "/a/bin:/z/wrappers:/usr/bin", "HOME": "/h", "CC": "cc"}
	for k, v := range want {
		if cur, _ := lookupVar(got, k); cur != v {
			t.Errorf("%s = %q, want %q", k, cur, v)
		}
	}
	if base[0] != "PATH=/usr/bin" {
		t.Errorf("input mutated: %q", base[0])
	}
}

// As root the substrate root is installed without --usermode, which apk refuses to root.
func TestRealize_RelocatableAsRoot(t *testing.T) {
	calls := fakeApk(t)
	emptyPath(t)
	asUID(t, 0)
	onAlpine(t, "3.24.0")
	cache := t.TempDir()
	if _, err := (&realizer{cacheDir: cache, apk: true}).Realize(context.Background(), []Need{{Capability: "git"}}); err != nil {
		t.Fatal(err)
	}
	add := strings.Join((*calls)[0], " ")
	if strings.Contains(add, "--usermode") || !strings.Contains(add, "--no-scripts") || !strings.Contains(add, "--root "+filepath.Join(cache, "root", "3.24.0")) {
		t.Fatalf("root install: %q, want --root and --no-scripts without --usermode", add)
	}
}
