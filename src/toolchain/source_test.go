package toolchain

import "testing"

// Download tools default to releaseBinarySource; govulncheck routes to GoInstallSource.
func TestToolDefSource_Selection(t *testing.T) {
	osv, ok := LookupTool("osv-scanner")
	if !ok {
		t.Fatal("osv-scanner not registered")
	}
	if _, ok := osv.source().(releaseBinarySource); !ok {
		t.Fatalf("osv-scanner should default to releaseBinarySource, got %T", osv.source())
	}
	gv, ok := LookupTool("govulncheck")
	if !ok {
		t.Fatal("govulncheck not registered")
	}
	if _, ok := gv.source().(GoInstallSource); !ok {
		t.Fatalf("govulncheck should use GoInstallSource, got %T", gv.source())
	}
}

func TestGoInstallRef(t *testing.T) {
	const mod = "golang.org/x/vuln/cmd/govulncheck"
	cases := map[string]string{
		"1.5.0":  mod + "@v1.5.0",
		"v1.5.0": mod + "@v1.5.0",
		"":       mod + "@latest",
	}
	for ver, want := range cases {
		if got := goInstallRef(mod, ver); got != want {
			t.Fatalf("goInstallRef(%q) = %q, want %q", ver, got, want)
		}
	}
}

// Materialize resolves Go through the toolchain, runs `go install module@vVERSION` with GOBIN
// pointed at the versioned bin dir, and reports go-install provenance (TOFU, no archive digest).
func TestGoInstallSource_Materialize(t *testing.T) {
	var gotBin string
	var gotArgs, gotEnv []string
	s := GoInstallSource{
		Module:    "golang.org/x/vuln/cmd/govulncheck",
		GoVersion: "1.26",
		resolveGo: func(rootDir, version string) (Result, error) {
			if version != "1.26" {
				t.Fatalf("expected pinned GoVersion 1.26, got %q", version)
			}
			return Result{Path: "/fake/go/bin/go"}, nil
		},
		run: func(goBin string, args, env []string) error {
			gotBin, gotArgs, gotEnv = goBin, args, env
			return nil
		},
	}
	sr, err := s.Materialize(SourceRequest{
		Version: "1.5.0",
		RootDir: "/repo",
		BinPath: "/cache/govulncheck/1.5.0/bin/govulncheck",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotBin != "/fake/go/bin/go" {
		t.Fatalf("go binary = %q, want the toolchain-resolved go", gotBin)
	}
	if len(gotArgs) != 2 || gotArgs[0] != "install" || gotArgs[1] != "golang.org/x/vuln/cmd/govulncheck@v1.5.0" {
		t.Fatalf("args = %v, want [install golang.org/x/vuln/cmd/govulncheck@v1.5.0]", gotArgs)
	}
	if !hasEnv(gotEnv, "GOBIN=/cache/govulncheck/1.5.0/bin") {
		t.Fatalf("GOBIN not pointed at the versioned bin dir; env=%v", gotEnv)
	}
	if !hasEnv(gotEnv, "GOFLAGS=-buildvcs=false") {
		t.Fatalf("GOFLAGS not set; env=%v", gotEnv)
	}
	if sr.Trust != TrustTOFU {
		t.Fatalf("trust = %q, want tofu (no upstream binary digest)", sr.Trust)
	}
	if sr.SourceURL != "go install golang.org/x/vuln/cmd/govulncheck@v1.5.0" {
		t.Fatalf("sourceURL = %q", sr.SourceURL)
	}
	if sr.SHA256 != "" {
		t.Fatalf("go install has no archive digest, got %q", sr.SHA256)
	}
}

func TestGoInstallSource_EmptyModule(t *testing.T) {
	if _, err := (GoInstallSource{}).Materialize(SourceRequest{}); err == nil {
		t.Fatal("empty module must error")
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
