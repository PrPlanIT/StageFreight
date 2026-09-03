package presetfetch

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

func TestHTTPFetcherFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.yml":
			if got := r.Header.Get("User-Agent"); got != userAgent {
				t.Errorf("User-Agent = %q, want %q", got, userAgent)
			}
			w.Write([]byte("lint:\n  level: full\n"))
		case "/missing.yml":
			w.WriteHeader(http.StatusNotFound)
		case "/binary.yml":
			w.Write([]byte{0xff, 0xfe, 0x00})
		case "/huge.yml":
			w.Write([]byte(strings.Repeat("a", maxPresetBytes+10)))
		}
	}))
	defer srv.Close()

	f := newHTTPFetcher()

	t.Run("returns the document", func(t *testing.T) {
		got, err := f.Fetch(srv.URL+"/ok.yml", "", "")
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if string(got) != "lint:\n  level: full\n" {
			t.Errorf("body = %q", got)
		}
	})

	// Failure must name the URL and the status; a preset that silently resolves to an
	// error page is worse than one that fails.
	t.Run("non-2xx is an error", func(t *testing.T) {
		_, err := f.Fetch(srv.URL+"/missing.yml", "", "")
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("err = %v, want one naming HTTP 404", err)
		}
	})

	t.Run("non-text is rejected as such", func(t *testing.T) {
		_, err := f.Fetch(srv.URL+"/binary.yml", "", "")
		if err == nil || !strings.Contains(err.Error(), "not text") {
			t.Fatalf("err = %v, want a not-text error", err)
		}
	})

	t.Run("oversized is bounded", func(t *testing.T) {
		_, err := f.Fetch(srv.URL+"/huge.yml", "", "")
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("err = %v, want a size-limit error", err)
		}
	})

	t.Run("unreachable host errors rather than hanging", func(t *testing.T) {
		if _, err := f.Fetch("http://127.0.0.1:1/x.yml", "", ""); err == nil {
			t.Fatal("want an error for an unreachable host")
		}
	})
}

// A URL carries no revision, so it is always tracked.
func TestHTTPFetcherClassify(t *testing.T) {
	k, err := newHTTPFetcher().Classify("https://example.org/x.yml", "main", "")
	if err != nil || k != presetref.Tracked {
		t.Fatalf("Classify = (%v, %v), want (tracked, nil)", k, err)
	}
}

// The dispatcher must route on the reference shape, not the scheme: a git source is
// frequently an https URL as well, and only the empty path distinguishes a bare URL.
func TestDispatchRoutesOnReferenceShape(t *testing.T) {
	d := &dispatchFetcher{git: stubFetcher("git"), http: stubFetcher("http")}
	cases := []struct{ source, path, want string }{
		{"https://example.org/foo.yml", "", "http"},                    // bare URL
		{"http://example.org/foo.yml", "", "http"},                     // bare URL, plaintext
		{"https://gitlab.example.com/Org/Repo", "preset/x.yml", "git"}, // git over https
		{"gitlab:Org/Repo", "preset/x.yml", "git"},                     // forge shorthand
		{"preset/x.yml", "", "git"},                                    // not a URL
	}
	for _, c := range cases {
		got, _ := d.Fetch(c.source, "", c.path)
		if string(got) != c.want {
			t.Errorf("Fetch(%q, path=%q) routed to %q, want %q", c.source, c.path, got, c.want)
		}
	}
}

type stubFetcher string

func (s stubFetcher) Fetch(_, _, _ string) ([]byte, error) { return []byte(s), nil }
func (s stubFetcher) Classify(_, _, _ string) (presetref.Kind, error) {
	return presetref.Tracked, nil
}

