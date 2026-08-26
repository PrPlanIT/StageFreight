package presetref

import (
	"errors"
	"testing"
)

// fakeFetcher records calls and returns scripted content/errors.
type fakeFetcher struct {
	content  []byte
	fetchErr error
	classify Kind
	classErr error
	fetches  int
}

func (f *fakeFetcher) Fetch(source, ref, path string) ([]byte, error) {
	f.fetches++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.content, nil
}
func (f *fakeFetcher) Classify(source, ref string) (Kind, error) {
	if f.classErr != nil {
		return Named, f.classErr
	}
	return f.classify, nil
}

// mapCache is an in-memory Cache.
type mapCache map[string][]byte

func (m mapCache) Read(key string) ([]byte, bool) { v, ok := m[key]; return v, ok }
func (m mapCache) Write(key string, content []byte) error {
	m[key] = content
	return nil
}

func TestResolve_TrackedFetchesLiveAndCaches(t *testing.T) {
	f := &fakeFetcher{content: []byte("fresh")}
	cache := mapCache{}
	r := Resolver{Fetcher: f, Cache: cache}

	got, err := r.Resolve(Parse("src//p.yml@refs/heads/main")) // Tracked
	if err != nil || string(got) != "fresh" {
		t.Fatalf("got %q, %v; want fresh", got, err)
	}
	if f.fetches != 1 {
		t.Errorf("fetches = %d, want 1", f.fetches)
	}
	if _, ok := cache.Read(CacheKey(Parse("src//p.yml@refs/heads/main"))); !ok {
		t.Error("tracked fetch should populate cache")
	}
}

func TestResolve_TrackedFallsBackToCacheOnFetchError(t *testing.T) {
	ref := Parse("src//p.yml@refs/heads/main")
	f := &fakeFetcher{fetchErr: errors.New("network down")}
	cache := mapCache{CacheKey(ref): []byte("stale")}
	warned := false
	r := Resolver{Fetcher: f, Cache: cache, Warnf: func(string, ...any) { warned = true }}

	got, err := r.Resolve(ref)
	if err != nil || string(got) != "stale" {
		t.Fatalf("got %q, %v; want stale fallback", got, err)
	}
	if !warned {
		t.Error("a stale-cache fallback should warn")
	}
}

func TestResolve_TrackedFailsWhenFetchErrorsAndNoCache(t *testing.T) {
	f := &fakeFetcher{fetchErr: errors.New("network down")}
	r := Resolver{Fetcher: f, Cache: mapCache{}}
	if _, err := r.Resolve(Parse("src//p.yml@refs/heads/main")); err == nil {
		t.Fatal("expected error when tracked fetch fails with no cache")
	}
}

func TestResolve_PinnedCacheAuthoritative(t *testing.T) {
	ref := Parse("src//p.yml@refs/tags/v1.0") // Pinned
	f := &fakeFetcher{content: []byte("should-not-fetch")}
	cache := mapCache{CacheKey(ref): []byte("cached")}
	r := Resolver{Fetcher: f, Cache: cache}

	got, err := r.Resolve(ref)
	if err != nil || string(got) != "cached" {
		t.Fatalf("got %q, %v; want cached", got, err)
	}
	if f.fetches != 0 {
		t.Errorf("pinned ref in cache must not fetch; fetches = %d", f.fetches)
	}
}

func TestResolve_PinnedFetchesOnceWhenAbsent(t *testing.T) {
	f := &fakeFetcher{content: []byte("v1-content")}
	cache := mapCache{}
	r := Resolver{Fetcher: f, Cache: cache}
	got, err := r.Resolve(Parse("src//p.yml@refs/tags/v1.0"))
	if err != nil || string(got) != "v1-content" {
		t.Fatalf("got %q, %v", got, err)
	}
	if f.fetches != 1 {
		t.Errorf("fetches = %d, want 1", f.fetches)
	}
}

func TestResolve_NamedClassifiesViaSource(t *testing.T) {
	ref := Parse("src//p.yml@release") // Named
	// Classify → Pinned means it should be cache-authoritative.
	f := &fakeFetcher{content: []byte("x"), classify: Pinned}
	cache := mapCache{CacheKey(ref): []byte("cached-pinned")}
	r := Resolver{Fetcher: f, Cache: cache}
	got, _ := r.Resolve(ref)
	if string(got) != "cached-pinned" || f.fetches != 0 {
		t.Errorf("Named→Pinned should serve cache without fetch; got %q fetches=%d", got, f.fetches)
	}

	// Classify → Tracked means it should fetch live.
	f2 := &fakeFetcher{content: []byte("fresh"), classify: Tracked}
	r2 := Resolver{Fetcher: f2, Cache: mapCache{}}
	got2, _ := r2.Resolve(Parse("src//p.yml@release"))
	if string(got2) != "fresh" || f2.fetches != 1 {
		t.Errorf("Named→Tracked should fetch live; got %q fetches=%d", got2, f2.fetches)
	}
}

func TestResolve_NamedClassifyFailureDegradesToTracked(t *testing.T) {
	f := &fakeFetcher{content: []byte("fresh"), classErr: errors.New("ls-remote failed")}
	warned := false
	r := Resolver{Fetcher: f, Cache: mapCache{}, Warnf: func(string, ...any) { warned = true }}
	got, err := r.Resolve(Parse("src//p.yml@somename"))
	if err != nil || string(got) != "fresh" {
		t.Fatalf("classify failure should degrade to tracked+fetch; got %q %v", got, err)
	}
	if !warned {
		t.Error("classify failure should warn")
	}
}

func TestResolve_OfflinePinnedFromCache_TrackedErrorsWhenMissing(t *testing.T) {
	pinned := Parse("src//p.yml@refs/tags/v1.0")
	cache := mapCache{CacheKey(pinned): []byte("cached")}
	r := Resolver{Fetcher: &fakeFetcher{fetchErr: errors.New("no net")}, Cache: cache, Mode: FetchOffline}

	got, err := r.Resolve(pinned)
	if err != nil || string(got) != "cached" {
		t.Errorf("offline pinned should serve cache; got %q %v", got, err)
	}
	if _, err := r.Resolve(Parse("src//p.yml@refs/heads/main")); err == nil {
		t.Error("offline tracked with no cache should error, not fetch")
	}
}

func TestResolve_LocalRefIsRejected(t *testing.T) {
	r := Resolver{Fetcher: &fakeFetcher{}, Cache: mapCache{}}
	if _, err := r.Resolve(Parse("preset/lint.yml")); err == nil {
		t.Error("a local ref must be rejected by the source resolver")
	}
}
