package layout

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
