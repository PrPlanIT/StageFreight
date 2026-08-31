package presetref

import "testing"

type stubFetch struct {
	body []byte
	err  error
}

func (s stubFetch) Fetch(_, _, _ string) ([]byte, error) { return s.body, s.err }
func (s stubFetch) Classify(_, _ string) (Kind, error)   { return Tracked, nil }

type memCache map[string][]byte

func (m memCache) Read(k string) ([]byte, bool) { v, ok := m[k]; return v, ok }
func (m memCache) Write(k string, v []byte) error {
	m[k] = v
	return nil
}

func TestTrackedResolutionReportsOutcome(t *testing.T) {
	ref := Parse("https://example.org/x.yml")

	t.Run("source moved is reported as drift", func(t *testing.T) {
		cache := memCache{CacheKey(ref): []byte("old")}
		var got Outcome
		r := Resolver{Fetcher: stubFetch{body: []byte("new")}, Cache: cache, Observe: func(o Outcome) { got = o }}
		body, err := r.Resolve(ref)
		if err != nil || string(body) != "new" {
			t.Fatalf("Resolve = (%q, %v)", body, err)
		}
		if !got.Fetched || !got.Drifted {
			t.Errorf("outcome = %+v, want fetched+drifted", got)
		}
		if string(cache[CacheKey(ref)]) != "new" {
			t.Error("retained copy must be refreshed to what the source served")
		}
	})

	t.Run("unchanged source is not drift", func(t *testing.T) {
		var got Outcome
		r := Resolver{
			Fetcher: stubFetch{body: []byte("same")},
			Cache:   memCache{CacheKey(ref): []byte("same")},
			Observe: func(o Outcome) { got = o },
		}
		if _, err := r.Resolve(ref); err != nil {
			t.Fatal(err)
		}
		if !got.Fetched || got.Drifted {
			t.Errorf("outcome = %+v, want fetched without drift", got)
		}
	})

	// A first resolution has nothing to compare against, so it is not drift.
	t.Run("first resolution is not drift", func(t *testing.T) {
		var got Outcome
		r := Resolver{Fetcher: stubFetch{body: []byte("first")}, Cache: memCache{}, Observe: func(o Outcome) { got = o }}
		if _, err := r.Resolve(ref); err != nil {
			t.Fatal(err)
		}
		if got.Drifted {
			t.Errorf("outcome = %+v, want no drift on first resolution", got)
		}
	})

	t.Run("unreachable source falls back and says so", func(t *testing.T) {
		var got Outcome
		r := Resolver{
			Fetcher: stubFetch{err: errFetch},
			Cache:   memCache{CacheKey(ref): []byte("retained")},
			Observe: func(o Outcome) { got = o },
		}
		body, err := r.Resolve(ref)
		if err != nil || string(body) != "retained" {
			t.Fatalf("Resolve = (%q, %v), want the retained copy", body, err)
		}
		if !got.Fallback || got.Fetched {
			t.Errorf("outcome = %+v, want fallback", got)
		}
	})
}

var errFetch = &fetchErr{}

type fetchErr struct{}

func (*fetchErr) Error() string { return "unreachable" }
