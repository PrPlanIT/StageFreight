package presetref

import (
	"errors"
	"testing"
	"time"
)

type revFetcher struct {
	body     []byte
	fetches  int
	fetchErr error
}

func (f *revFetcher) Fetch(_, _, _ string) ([]byte, error) {
	f.fetches++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.body, nil
}
func (f *revFetcher) Classify(_, _, _ string) (Kind, error) { return Tracked, nil }

// countingCache counts retentions, which is what decides whether a run leaves a tree
// dirty and turns a no-op into a commit.
type countingCache struct {
	mapCache
	writes int
}

func (c *countingCache) Write(key string, content []byte) error {
	c.writes++
	return c.mapCache.Write(key, content)
}

// A source serving what is already retained must leave the tree alone. Retaining it
// again is what makes every run a commit in every satellite carrying the cache.
func TestUnchangedSourceIsNotRewritten(t *testing.T) {
	ref := Parse("src//p.yml@refs/heads/main")
	f := &revFetcher{body: []byte("v1")}
	cache := &countingCache{mapCache: mapCache{}}
	r := Resolver{Fetcher: f, Cache: cache}

	if _, err := r.Resolve(ref); err != nil { // first: nothing retained, must retain
		t.Fatal(err)
	}
	if cache.writes != 1 {
		t.Fatalf("first resolve writes = %d, want 1", cache.writes)
	}

	var got Outcome
	r.Observe = func(o Outcome) { got = o }
	if body, err := r.Resolve(ref); err != nil || string(body) != "v1" {
		t.Fatalf("second resolve = %q, %v", body, err)
	}
	if cache.writes != 1 {
		t.Errorf("unchanged source rewritten; writes = %d, want still 1", cache.writes)
	}
	if !got.Unchanged {
		t.Errorf("outcome = %+v, want Unchanged", got)
	}
	if got.Refreshed {
		t.Errorf("outcome = %+v, want no Refreshed: nothing was written", got)
	}
}

// Changed content must be retained, or the cache would hide the very change it exists to
// carry.
func TestChangedSourceIsRetained(t *testing.T) {
	ref := Parse("src//p.yml@refs/heads/main")
	f := &revFetcher{body: []byte("v1")}
	cache := &countingCache{mapCache: mapCache{}}
	r := Resolver{Fetcher: f, Cache: cache}
	if _, err := r.Resolve(ref); err != nil {
		t.Fatal(err)
	}
	f.body = []byte("v2")
	body, err := r.Resolve(ref)
	if err != nil || string(body) != "v2" {
		t.Fatalf("got %q, %v; want the new content", body, err)
	}
	if cache.writes != 2 {
		t.Errorf("writes = %d, want 2", cache.writes)
	}
}

type agedCache struct {
	mapCache
	age time.Duration
}

func (a agedCache) Age(string) (time.Duration, bool) { return a.age, true }

// A fallback with no expiry is a freeze nobody declared: the source stays unreachable,
// and every consumer keeps building on old content with only a warning.
func TestFallbackIsBounded(t *testing.T) {
	ref := Parse("src//p.yml@refs/heads/main")
	key := CacheKey(ref)

	t.Run("within the bound it stands in", func(t *testing.T) {
		c := agedCache{mapCache: mapCache{key: []byte("retained")}, age: time.Hour}
		r := Resolver{
			Fetcher:        &revFetcher{fetchErr: errors.New("unreachable")},
			Cache:          c,
			MaxFallbackAge: 24 * time.Hour,
		}
		got, err := r.Resolve(ref)
		if err != nil || string(got) != "retained" {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("past the bound it stops pretending", func(t *testing.T) {
		c := agedCache{mapCache: mapCache{key: []byte("retained")}, age: 30 * 24 * time.Hour}
		r := Resolver{
			Fetcher:        &revFetcher{fetchErr: errors.New("unreachable")},
			Cache:          c,
			MaxFallbackAge: 24 * time.Hour,
		}
		if _, err := r.Resolve(ref); err == nil {
			t.Fatal("a retained copy past the bound must not silently stand in")
		}
	})

	t.Run("no bound set keeps the old behaviour", func(t *testing.T) {
		c := agedCache{mapCache: mapCache{key: []byte("retained")}, age: 365 * 24 * time.Hour}
		r := Resolver{Fetcher: &revFetcher{fetchErr: errors.New("unreachable")}, Cache: c}
		if _, err := r.Resolve(ref); err != nil {
			t.Fatalf("unbounded fallback must still work: %v", err)
		}
	})
}
