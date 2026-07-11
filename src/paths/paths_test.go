package paths

import "testing"

func TestDurable(t *testing.T) {
	if got := Durable(""); got != ".stagefreight" {
		t.Errorf("Durable(\"\") = %q, want .stagefreight", got)
	}
	if got := Durable("", "badges", "build.svg"); got != ".stagefreight/badges/build.svg" {
		t.Errorf("Durable rel = %q", got)
	}
	if got := Durable("/repo", "toolchains.lock"); got != "/repo/.stagefreight/toolchains.lock" {
		t.Errorf("Durable anchored = %q", got)
	}
}

func TestEphemeral(t *testing.T) {
	// Ephemeral resolves flat under the namespace — same path shape as Durable, opposite
	// lifecycle (gitignored). Value-preserving with the old .stagefreight/<x> literals.
	if got := Ephemeral("", "reports"); got != ".stagefreight/reports" {
		t.Errorf("Ephemeral rel = %q", got)
	}
	if got := Ephemeral("", "security", "advisories.json"); got != ".stagefreight/security/advisories.json" {
		t.Errorf("Ephemeral nested = %q", got)
	}
	if got := Ephemeral("/repo", "outputs.json"); got != "/repo/.stagefreight/outputs.json" {
		t.Errorf("Ephemeral anchored = %q", got)
	}
	// Ephemeral and Durable coincide in path today (the distinction is the gitignore
	// allowlist, not the location) — lock that so a divergence is a deliberate choice.
	if Ephemeral("", "x") != Durable("", "x") {
		t.Error("Ephemeral and Durable must resolve identically until ephemeral is relocated")
	}
}

func TestScratch(t *testing.T) {
	if got := Scratch(""); got != ".stagefreight/.tmp" {
		t.Errorf("Scratch(\"\") = %q, want .stagefreight/.tmp", got)
	}
	if got := Scratch("", "outputs.json"); got != ".stagefreight/.tmp/outputs.json" {
		t.Errorf("Scratch rel = %q", got)
	}
	if got := Scratch("/repo", "reports"); got != "/repo/.stagefreight/.tmp/reports" {
		t.Errorf("Scratch anchored = %q", got)
	}
	if ScratchRelDir() != ".stagefreight/.tmp" {
		t.Errorf("ScratchRelDir = %q", ScratchRelDir())
	}
}

func TestCache(t *testing.T) {
	// Empty cacheRoot falls back to the default mount; it is never under the repo tree.
	if got := Cache("", "toolchains", "trivy"); got != "/stagefreight/toolchains/trivy" {
		t.Errorf("Cache default = %q", got)
	}
	if got := Cache("/opt/runner/sf", "gomodcache"); got != "/opt/runner/sf/gomodcache" {
		t.Errorf("Cache explicit = %q", got)
	}
	if ResolveCacheRoot("") != DefaultCacheRoot {
		t.Errorf("ResolveCacheRoot(\"\") = %q, want %q", ResolveCacheRoot(""), DefaultCacheRoot)
	}
	if ResolveCacheRoot("/x") != "/x" {
		t.Error("ResolveCacheRoot must honor an explicit path")
	}
}

func TestState(t *testing.T) {
	if got := State("kms", "key"); got != "/var/lib/stagefreight/kms/key" {
		t.Errorf("State = %q", got)
	}
}
