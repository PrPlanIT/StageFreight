package build

import "testing"

// Target is the canonical build-target identity. The load-bearing guarantee for the
// seam refactor: a libc-LESS target (every Go target today) renders byte-identically
// to the old Platform — same String, same Slug, same step ID — so introducing the
// canonical model with Libc/ABI changes nothing for existing Go builds. Libc only
// appears in derived names when it is actually set (musl, later).
func TestTarget_LibcLessIsBehaviorPreserving(t *testing.T) {
	tgt := ParseTarget("linux/amd64")
	if tgt.OS != "linux" || tgt.Arch != "amd64" || tgt.Libc != "" {
		t.Fatalf("parse: %+v", tgt)
	}
	if got := tgt.String(); got != "linux/amd64" {
		t.Errorf("String libc-less must be os/arch, got %q", got)
	}
	if got := tgt.Slug(); got != "linux-amd64" {
		t.Errorf("Slug libc-less must be os-arch, got %q", got)
	}
	if got := StepIDForTarget("myapp", tgt); got != "myapp--linux-amd64" {
		t.Errorf("step ID libc-less must be unchanged, got %q", got)
	}
}

// When a libc IS set, it becomes part of the canonical identity and every derived
// name — String, Slug, step ID — so gnu and musl are distinct targets.
func TestTarget_LibcDisambiguates(t *testing.T) {
	tgt := ParseTarget("linux/amd64/musl")
	if tgt.OS != "linux" || tgt.Arch != "amd64" || tgt.Libc != "musl" {
		t.Fatalf("parse: %+v", tgt)
	}
	if got := tgt.String(); got != "linux/amd64/musl" {
		t.Errorf("String with libc, got %q", got)
	}
	if got := tgt.Slug(); got != "linux-amd64-musl" {
		t.Errorf("Slug with libc, got %q", got)
	}
	if got := StepIDForTarget("myapp", tgt); got != "myapp--linux-amd64-musl" {
		t.Errorf("step ID with libc, got %q", got)
	}

	// gnu and musl on the same os/arch are NOT the same target.
	gnu := ParseTarget("linux/amd64/gnu")
	if gnu.Slug() == tgt.Slug() {
		t.Error("gnu and musl must produce distinct slugs")
	}
}

func TestParseTargets(t *testing.T) {
	got := ParseTargets([]string{"linux/amd64", "darwin/arm64", "linux/amd64/musl"})
	if len(got) != 3 || got[2].Libc != "musl" {
		t.Fatalf("parse targets: %+v", got)
	}
}
