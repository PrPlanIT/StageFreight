package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

type fakeSrcFetcher struct{ content []byte }

func (f fakeSrcFetcher) Fetch(source, ref, path string) ([]byte, error) { return f.content, nil }
func (f fakeSrcFetcher) Classify(source, ref, _ string) (presetref.Kind, error) {
	return presetref.Tracked, nil
}

// TestSourceAwareLoader covers the three resolution paths loadResolved now uses: a local
// path reads the working tree; a sourced ref with SourceFetcher wired fetches; a sourced
// ref with no fetcher and an empty cache errors clearly (offline miss).
func TestSourceAwareLoader(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "preset"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "preset", "lint.yml"), []byte("local-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := sourceAwareLoader{local: localPresetLoader{baseDir: dir}, cacheDir: filepath.Join(dir, "cache")}

	// Local path → working tree.
	if got, err := l.Load("preset/lint.yml"); err != nil || string(got) != "local-content" {
		t.Fatalf("local: got %q, %v", got, err)
	}

	// Sourced ref, fetcher wired → fetched content.
	old := SourceFetcher
	defer func() { SourceFetcher = old }()
	SourceFetcher = fakeSrcFetcher{content: []byte("remote-content")}
	if got, err := l.Load("gitlab:Org/Repo//preset/lint.yml@refs/heads/main"); err != nil || string(got) != "remote-content" {
		t.Fatalf("sourced+fetcher: got %q, %v", got, err)
	}

	// Sourced ref, no fetcher, empty cache → clear error (offline miss), not a silent pass.
	SourceFetcher = nil
	if _, err := l.Load("gitlab:Org/Repo//preset/lint.yml@refs/tags/v9"); err == nil {
		t.Fatal("sourced ref with no fetcher and empty cache should error")
	}
}

// TestResolveSource covers forge-shorthand → URL resolution from the config's forges: a
// known forge id maps to its base URL; a URL, an scp-like remote, or an unknown id passes
// through (the fetcher then errors clearly on the unresolvable ones).
func TestResolveSource(t *testing.T) {
	l := sourceAwareLoader{forges: map[string]string{"gitlab": "https://gl.example/"}}
	cases := []struct{ in, want string }{
		{"gitlab:Org/Repo", "https://gl.example/Org/Repo"},
		{"https://host/org/repo", "https://host/org/repo"},
		{"git@host:org/repo", "git@host:org/repo"}, // scp-like, unknown id → passthrough
		{"unknown:foo/bar", "unknown:foo/bar"},     // unknown forge id → passthrough
	}
	for _, c := range cases {
		if got := l.resolveSource(c.in); got != c.want {
			t.Errorf("resolveSource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
