package toolchain

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// TestRegistry_PlatformURLs: every released-binary tool resolves an asset for linux/amd64
// and linux/arm64 in its project's own naming, and none for a platform it doesn't publish.
func TestRegistry_PlatformURLs(t *testing.T) {
	want := map[string][2]string{ // tool → amd64 token, arm64 token
		"trivy":       {"_Linux-64bit.tar.gz", "_Linux-ARM64.tar.gz"},
		"syft":        {"_linux_amd64.tar.gz", "_linux_arm64.tar.gz"},
		"grype":       {"_linux_amd64.tar.gz", "_linux_arm64.tar.gz"},
		"osv-scanner": {"osv-scanner_linux_amd64", "osv-scanner_linux_arm64"},
		"cosign":      {"cosign-linux-amd64", "cosign-linux-arm64"},
		"flux":        {"_linux_amd64.tar.gz", "_linux_arm64.tar.gz"},
		"kubectl":     {"/bin/linux/amd64/kubectl", "/bin/linux/arm64/kubectl"},
		"kustomize":   {"_linux_amd64.tar.gz", "_linux_arm64.tar.gz"},
		"kubeconform": {"kubeconform-linux-amd64.tar.gz", "kubeconform-linux-arm64.tar.gz"},
	}
	for tool, toks := range want {
		def, ok := LookupTool(tool)
		if !ok {
			t.Fatalf("%s not registered", tool)
		}
		for i, arch := range []string{"amd64", "arm64"} {
			url := def.DownloadURL("1.2.3", Target{GOOS: "linux", GOARCH: arch})
			if !strings.Contains(url, toks[i]) {
				t.Errorf("%s linux/%s url = %q, want it to contain %q", tool, arch, url, toks[i])
			}
		}
		if url := def.DownloadURL("1.2.3", Target{GOOS: "linux", GOARCH: "riscv64"}); url != "" {
			t.Errorf("%s must publish no linux/riscv64 build here, got %q", tool, url)
		}
		if url := def.DownloadURL("1.2.3", Target{GOOS: "darwin", GOARCH: "arm64"}); url != "" {
			t.Errorf("%s must publish no darwin build here, got %q", tool, url)
		}
	}
}

// TestRegistry_RustTriples: the Rust tools publish per-libc builds named by target triple.
func TestRegistry_RustTriples(t *testing.T) {
	for _, tool := range []string{"cargo-llvm-cov", "cargo-nextest"} {
		def, _ := LookupTool(tool)
		if len(def.Libcs) != 2 {
			t.Fatalf("%s must declare gnu and musl builds, got %v", tool, def.Libcs)
		}
		cases := map[Target]string{
			{GOOS: "linux", GOARCH: "amd64", Libc: "gnu"}:  "x86_64-unknown-linux-gnu",
			{GOOS: "linux", GOARCH: "amd64", Libc: "musl"}: "x86_64-unknown-linux-musl",
			{GOOS: "linux", GOARCH: "arm64", Libc: "gnu"}:  "aarch64-unknown-linux-gnu",
			{GOOS: "linux", GOARCH: "arm64", Libc: "musl"}: "aarch64-unknown-linux-musl",
		}
		for tg, triple := range cases {
			if url := def.DownloadURL("0.9.1", tg); !strings.Contains(url, triple) {
				t.Errorf("%s %+v url = %q, want %s", tool, tg, url, triple)
			}
		}
		if url := def.DownloadURL("0.9.1", Target{GOOS: "linux", GOARCH: "amd64"}); url != "" {
			t.Errorf("%s with no libc chosen must not resolve, got %q", tool, url)
		}
	}
	nextest, _ := LookupTool("cargo-nextest")
	if got := nextest.ChecksumURL("0.9.1", Target{GOOS: "linux", GOARCH: "arm64", Libc: "musl"}); !strings.HasSuffix(got, "aarch64-unknown-linux-musl.sha256") {
		t.Errorf("nextest checksum url = %q, want the matching triple's .sha256", got)
	}
}

func TestVariant(t *testing.T) {
	trivy, _ := LookupTool("trivy")
	if v, err := Variant(trivy, ""); err != nil || v != "" {
		t.Errorf("a static tool has no variant, got %q, %v", v, err)
	}
	if _, err := Variant(trivy, "musl"); err == nil || !strings.Contains(err.Error(), "no libc variants") {
		t.Errorf("variant on a static tool must fail, got %v", err)
	}

	nextest, _ := LookupTool("cargo-nextest")
	if v, err := Variant(nextest, "musl"); err != nil || v != "musl" {
		t.Errorf("explicit musl = %q, %v", v, err)
	}
	if v, err := Variant(nextest, ""); err != nil || v != HostLibc() {
		t.Errorf("default must be the host libc %q, got %q, %v", HostLibc(), v, err)
	}
	if _, err := Variant(nextest, "uclibc"); err == nil || !strings.Contains(err.Error(), "no uclibc build") {
		t.Errorf("an unpublished variant must fail, got %v", err)
	}
}

func TestPlatformKey(t *testing.T) {
	base := runtime.GOOS + "/" + runtime.GOARCH
	if PlatformKey("") != base || PlatformKey("gnu") != base {
		t.Errorf("static and gnu keys must be %s, got %q / %q", base, PlatformKey(""), PlatformKey("gnu"))
	}
	if PlatformKey("musl") != base+"-musl" {
		t.Errorf("musl key = %q, want %s-musl", PlatformKey("musl"), base)
	}
}

// TestSelect_Variant: variant: picks the build, and the digest is looked up under that
// build's key.
func TestSelect_Variant(t *testing.T) {
	musl := PlatformKey("musl")
	desired := map[string]config.ToolConstraint{"cargo-nextest": {
		Constraint: "0.9.1", Variant: "musl",
		SHA256: map[string]string{musl: "muslasserted"},
	}}
	sel, err := Select(t.TempDir(), "cargo-nextest", desired)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Variant != "musl" || sel.SHA256 != "muslasserted" {
		t.Errorf("got %+v, want variant musl with the musl digest", sel)
	}

	_, err = Select(t.TempDir(), "trivy", map[string]config.ToolConstraint{"trivy": {Constraint: "0.x", Variant: "musl"}})
	if err == nil || !strings.Contains(err.Error(), "toolchains.want.trivy.variant") {
		t.Errorf("variant on a static tool must fail naming the field, got %v", err)
	}
}

// TestResolveWithDef_VariantsAndCachedDigest: musl and glibc builds of one version install
// side by side, and a cached artifact is re-checked against an asserted digest.
func TestResolveWithDef_VariantsAndCachedDigest(t *testing.T) {
	bodies := map[string][]byte{"gnu": []byte("#!/bin/sh\necho gnu\n"), "musl": []byte("#!/bin/sh\necho musl\n")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		libc := r.URL.Path[strings.LastIndex(r.URL.Path, "-")+1:]
		_, _ = w.Write(bodies[libc])
	}))
	defer srv.Close()
	def := ToolDef{
		Name: "fakevariant", BinaryName: "fakevariant", Format: "binary", Libcs: []string{"gnu", "musl"},
		DownloadURL: func(v string, tg Target) string { return srv.URL + "/fakevariant-" + v + "-" + tg.Libc },
	}
	digest := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

	t.Setenv(paths.CacheRootEnv, t.TempDir())
	root := t.TempDir()
	gnu, err := resolveWithDef(root, def, "1.0.0", "gnu", "")
	if err != nil {
		t.Fatalf("gnu: %v", err)
	}
	musl, err := resolveWithDef(root, def, "1.0.0", "musl", "")
	if err != nil {
		t.Fatalf("musl: %v", err)
	}
	if gnu.Path == musl.Path {
		t.Fatalf("gnu and musl builds must install apart, both at %s", gnu.Path)
	}
	if musl.SHA256 != digest(bodies["musl"]) || gnu.SHA256 != digest(bodies["gnu"]) {
		t.Errorf("each variant must record its own artifact digest")
	}

	// The gnu build is cached; asserting the musl digest for it must not be satisfied by
	// the cache — it re-downloads, and the gnu artifact then fails verification.
	_, err = resolveWithDef(root, def, "1.0.0", "gnu", digest(bodies["musl"]))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("a cached artifact must be re-checked against an asserted digest, got %v", err)
	}
	if res, err := resolveWithDef(root, def, "1.0.0", "musl", digest(bodies["musl"])); err != nil || !res.CacheHit {
		t.Errorf("a cached artifact matching the asserted digest is a cache hit, got %+v, %v", res, err)
	}
}

// TestSource_NoBuildForPlatform: a platform the tool doesn't publish fails clearly
// instead of downloading another platform's binary.
func TestSource_NoBuildForPlatform(t *testing.T) {
	def := ToolDef{Name: "noarch", BinaryName: "noarch", Format: "binary",
		DownloadURL: func(v string, tg Target) string { return "" }}
	_, err := releaseBinarySource{}.Materialize(SourceRequest{Def: def, Version: "1.0.0", GOOS: "linux", GOARCH: "riscv64"})
	if err == nil || !strings.Contains(err.Error(), "publishes no build") {
		t.Errorf("must fail with publishes no build, got %v", err)
	}
}
