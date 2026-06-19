package engines

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// The Rust engine plans into the SAME step shape as Go — registered under binary-rust,
// host target, the canonical DistDir output path — so a Rust binary enters the shared
// pipeline identically. (The real cargo build is validated in CI, not here.)
func TestRustEngine_Plan(t *testing.T) {
	eng, err := build.GetV2("binary-rust")
	if err != nil {
		t.Fatalf("the Rust engine must register under binary-rust: %v", err)
	}
	if eng.Name() != "binary-rust" {
		t.Errorf("engine name: %q", eng.Name())
	}

	host := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	steps, err := eng.Plan(context.Background(), build.BuildConfig{
		ID: "app", Kind: "binary", Builder: "rust", From: ".", Output: "app",
		Platforms: []build.Target{host},
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("host-only: expected one step, got %d", len(steps))
	}
	s := steps[0]
	if s.Engine != "binary-rust" {
		t.Errorf("step.Engine (dispatch key): %q", s.Engine)
	}
	if s.Target != host {
		t.Errorf("target must be the host: %+v", s.Target)
	}
	if got := s.Outputs[0].Path; got != build.DistDir+"/"+host.Slug()+"/app" {
		t.Errorf("output path must use the canonical DistDir layout: %q", got)
	}
	meta, ok := s.Meta.(RustMeta)
	if !ok || meta.BinName != "app" || !meta.Release {
		t.Errorf("meta: %+v", s.Meta)
	}
}

func TestRustEngine_RejectsCrossAndNonRust(t *testing.T) {
	eng, _ := build.GetV2("binary-rust")

	// A non-host platform is rejected, not silently built for the host (cross is later).
	if _, err := eng.Plan(context.Background(), build.BuildConfig{
		ID: "x", Builder: "rust", From: ".", Output: "x",
		Platforms: []build.Target{{OS: "plan9", Arch: "mips"}},
	}); err == nil {
		t.Error("cross-compilation must be rejected (host-only for now)")
	}

	// A non-rust builder is rejected (the contributor dispatches the right engine).
	if _, err := eng.Plan(context.Background(), build.BuildConfig{
		ID: "x", Builder: "go", From: ".",
		Platforms: []build.Target{{OS: runtime.GOOS, Arch: runtime.GOARCH}},
	}); err == nil {
		t.Error("the Rust engine must reject a non-rust builder")
	}
}

func TestDetectCrateBinName(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[package]\nname = \"mytool\"\nversion = \"0.1.0\"\n")
	if got := detectCrateBinName(dir); got != "mytool" {
		t.Errorf("got %q", got)
	}
	// a `name` in another section must not be mistaken for the package name.
	write("[dependencies]\nname = \"notit\"\n\n[package]\nname = \"realname\"\n")
	if got := detectCrateBinName(dir); got != "realname" {
		t.Errorf("got %q", got)
	}
	// no Cargo.toml → empty (caller errors / requires explicit output).
	if got := detectCrateBinName(t.TempDir()); got != "" {
		t.Errorf("missing manifest should yield empty, got %q", got)
	}
}
