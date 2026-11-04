package toolchain

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"

	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/substrate"
)

// zigTarball builds an xz tarball shaped like a Zig release: a top-level dir holding
// the zig binary and lib/.
func zigTarball(t *testing.T, version string) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	top := fmt.Sprintf("zig-%s-%s/", zigPlatform(runtime.GOOS, runtime.GOARCH), version)
	files := map[string]string{top + "zig": "#!/bin/sh\necho zig\n", top + "lib/std/std.zig": "// std\n"}
	for name, body := range files {
		mode := int64(0o644)
		if strings.HasSuffix(name, "/zig") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	xw, err := xz.NewWriter(&out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// zigServer serves a release index and a tarball; shasum overrides the index's digest.
func zigServer(t *testing.T, version string, tarball []byte, shasum string) {
	t.Helper()
	if shasum == "" {
		sum := sha256.Sum256(tarball)
		shasum = hex.EncodeToString(sum[:])
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			fmt.Fprintf(w, `{"%s":{"%s":{"tarball":"%s/zig.tar.xz","shasum":"%s"}}}`,
				version, zigPlatform(runtime.GOOS, runtime.GOARCH), srv.URL, shasum)
			return
		}
		_, _ = w.Write(tarball)
	}))
	t.Cleanup(srv.Close)
	old := zigIndexURL
	zigIndexURL = srv.URL + "/index.json"
	t.Cleanup(func() { zigIndexURL = old })
}

// Zig installs from its release index, verified against the published sha256, with
// lib/ beside the binary; the second resolve is a verified cache hit.
func TestResolveZig_VerifiedInstallAndCacheHit(t *testing.T) {
	if zigPlatform(runtime.GOOS, runtime.GOARCH) == "" {
		t.Skip("no zig build for this platform")
	}
	zigServer(t, "9.9.9", zigTarball(t, "9.9.9"), "")
	t.Setenv(paths.CacheRootEnv, t.TempDir())

	r, err := resolveZig(t.TempDir(), "9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if r.CacheHit || r.Trust != TrustChecksum {
		t.Fatalf("first resolve: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(r.Path), "lib", "std", "std.zig")); err != nil {
		t.Fatalf("lib/ must sit beside the binary: %v", err)
	}
	again, err := resolveZig(t.TempDir(), "9.9.9")
	if err != nil || !again.CacheHit || again.Path != r.Path {
		t.Fatalf("second resolve: %+v %v", again, err)
	}
}

// A tarball that doesn't match the published digest is refused and not installed.
func TestResolveZig_ChecksumMismatchRefuses(t *testing.T) {
	if zigPlatform(runtime.GOOS, runtime.GOARCH) == "" {
		t.Skip("no zig build for this platform")
	}
	zigServer(t, "9.9.9", zigTarball(t, "9.9.9"), strings.Repeat("0", 64))
	cache := t.TempDir()
	t.Setenv(paths.CacheRootEnv, cache)

	if _, err := resolveZig(t.TempDir(), "9.9.9"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "toolchains", "zig", "9.9.9", "zig")); err == nil {
		t.Fatal("a mismatched tarball must not be installed")
	}
}

func TestZigRelease(t *testing.T) {
	index := []byte(`{"master":{"x86_64-linux":{"tarball":"m","shasum":"x"}},"0.17.1":{"x86_64-linux":{"tarball":"u","shasum":"s"},"date":"2026-01-01"}}`)
	a, err := zigRelease(index, "0.17.1", "x86_64-linux")
	if err != nil || a.Tarball != "u" || a.Shasum != "s" {
		t.Fatalf("got %+v %v", a, err)
	}
	if _, err := zigRelease(index, "0.17.1", "aarch64-linux"); err == nil {
		t.Error("a missing platform must be an error")
	}
	if _, err := zigRelease(index, "0.1.0", "x86_64-linux"); err == nil {
		t.Error("a missing version must be an error")
	}
}

// The wrappers present zig as the usual compiler driver names and find zig from their
// own path, so a cache mounted elsewhere still runs.
func TestWriteZigWrappers(t *testing.T) {
	install := filepath.Join(t.TempDir(), "a")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "zig"), []byte("#!/bin/sh\necho \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeZigWrappers(filepath.Join(install, "wrappers")); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(filepath.Dir(install), "b")
	if err := os.Rename(install, moved); err != nil {
		t.Fatal(err)
	}
	for name, sub := range map[string]string{"cc": "cc", "gcc": "cc", "c++": "c++", "g++": "c++", "ar": "ar", "ranlib": "ranlib"} {
		out, err := exec.Command(filepath.Join(moved, "wrappers", name), "-o", "a b").Output()
		if want := sub + " -o a b"; err != nil || strings.TrimSpace(string(out)) != want {
			t.Errorf("%s: %q %v, want %q", name, out, err, want)
		}
	}
}

// Without a host cc the provisioned zig is put on PATH as the C compiler.
func TestRealizeCToolchain(t *testing.T) {
	if zigPlatform(runtime.GOOS, runtime.GOARCH) == "" {
		t.Skip("no zig build for this platform")
	}
	n := substrate.Need{Capability: "c-toolchain", Source: "go test -race"}
	zigServer(t, defaultZigVersion, zigTarball(t, defaultZigVersion), "")
	t.Setenv(paths.CacheRootEnv, t.TempDir())
	root := t.TempDir()
	r, err := realizeCToolchain(context.Background(), root, n)
	if err != nil || r.Backend != "zig" || r.Env["CC"] != "cc" || len(r.PathDirs) != 1 {
		t.Fatalf("zig: %+v %v", r, err)
	}
	if out, err := exec.Command(filepath.Join(r.PathDirs[0], "cc")).Output(); err != nil || strings.TrimSpace(string(out)) != "zig" {
		t.Fatalf("cc wrapper: %q %v, want it to run the provisioned zig", out, err)
	}
}
