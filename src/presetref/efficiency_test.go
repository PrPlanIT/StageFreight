package presetref

import (
	"errors"
	"testing"
	"time"
)

type revFetcher struct {
	body     []byte
	rev      string
	fetches  int
	revCalls int
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
func (f *revFetcher) Revision(_, _, _ string) (string, error) {
	f.revCalls++
	return f.rev, nil
}

// Asking what the source points at is cheap; transferring content is not. When the
// answer matches what was retained, nothing needs to move.
func TestUnchangedSourceIsNotRefetched(t *testing.T) {
	ref := Parse("src//p.yml@refs/heads/main")
	f := &revFetcher{body: []byte("v1"), rev: "abc123"}
	cache := mapCache{}
	r := Resolver{Fetcher: f, Cache: cache}

	if _, err := r.Resolve(ref); err != nil { // first: nothing retained, must fetch
		t.Fatal(err)
	}
	if f.fetches != 1 {
		t.Fatalf("first resolve fetches = %d, want 1", f.fetches)
	}

	var got Outcome
	r.Observe = func(o Outcome) { got = o }
	if body, err := r.Resolve(ref); err != nil || string(body) != "v1" {
		t.Fatalf("second resolve = %q, %v", body, err)
	}
	if f.fetches != 1 {
		t.Errorf("unchanged source refetched; fetches = %d, want still 1", f.fetches)
	}
	if !got.Unchanged {
		t.Errorf("outcome = %+v, want Unchanged", got)
	}
}

// A moved revision must still transfer, or the check would hide the very change it
// exists to notice.
func TestMovedRevisionRefetches(t *testing.T) {
	ref := Parse("src//p.yml@refs/heads/main")
	f := &revFetcher{body: []byte("v1"), rev: "abc123"}
	r := Resolver{Fetcher: f, Cache: mapCache{}}
	if _, err := r.Resolve(ref); err != nil {
		t.Fatal(err)
	}
	f.rev, f.body = "def456", []byte("v2")
	body, err := r.Resolve(ref)
	if err != nil || string(body) != "v2" {
		t.Fatalf("got %q, %v; want the new content", body, err)
	}
	if f.fetches != 2 {
		t.Errorf("fetches = %d, want 2", f.fetches)
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
