package presetref

import "testing"

func TestFSCache_RoundTrip(t *testing.T) {
	c := NewFSCache(t.TempDir())

	if _, ok := c.Read("missing/key.yml"); ok {
		t.Error("Read of an absent key should miss")
	}

	key := CacheKey(Parse("gitlab:Org/Repo//preset/lint.yml@refs/tags/v1.0"))
	if err := c.Write(key, []byte("content")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, ok := c.Read(key)
	if !ok || string(got) != "content" {
		t.Errorf("Read after Write = %q, %v; want content", got, ok)
	}
}

// TestFSCache_ResolverIntegration wires the real FSCache into the Resolver: a pin is
// re-checked against its source on every resolve, and agreeing content resolves clean.
func TestFSCache_ResolverIntegration(t *testing.T) {
	f := &fakeFetcher{content: []byte("v1")}
	c := NewFSCache(t.TempDir())
	r := Resolver{Fetcher: f, Cache: c}
	ref := Parse("gitlab:Org/Repo//preset/lint.yml@refs/tags/v1.0")

	if got, err := r.Resolve(ref); err != nil || string(got) != "v1" {
		t.Fatalf("first resolve = %q, %v", got, err)
	}
	if got, err := r.Resolve(ref); err != nil || string(got) != "v1" {
		t.Fatalf("second resolve = %q, %v", got, err)
	}
	if f.fetches != 2 {
		t.Errorf("pin checked %d times; want 2 — a pin unverified is a pin unenforced", f.fetches)
	}
}
