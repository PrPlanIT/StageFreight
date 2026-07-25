package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDescriptionRoutingNoMixing locks the semantic (short, full) → field routing so it can
// never regress into the name-match trap: a short tagline dumped into a markdown-body field,
// or a readme stuffed into a short-description field. Harbor/Quay have ONE markdown field
// (must get the README body); JFrog's config field is a short description (must get short,
// never the readme). See src/postbuild/metadata.go + the kind: metadata design.
func TestDescriptionRoutingNoMixing(t *testing.T) {
	const short, full = "SHORT_TAGLINE", "FULL_README_BODY"

	capture := func() (*httptest.Server, *map[string]any) {
		got := map[string]any{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &got)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
		}))
		return srv, &got
	}

	t.Run("harbor single field takes the README, never the short tagline", func(t *testing.T) {
		srv, got := capture()
		defer srv.Close()
		h := &Harbor{client: httpClient{base: srv.URL}, baseURL: srv.URL}
		if err := h.UpdateDescription(context.Background(), "proj/repo", short, full); err != nil {
			t.Fatal(err)
		}
		if (*got)["description"] != full {
			t.Fatalf("harbor description = %v, want the README body %q (never the short tagline)", (*got)["description"], full)
		}
	})

	t.Run("quay single markdown field takes the README, never the short tagline", func(t *testing.T) {
		srv, got := capture()
		defer srv.Close()
		q := &Quay{client: httpClient{base: srv.URL}, baseURL: srv.URL}
		if err := q.UpdateDescription(context.Background(), "org/repo", short, full); err != nil {
			t.Fatal(err)
		}
		if (*got)["description"] != full {
			t.Fatalf("quay description = %v, want the README body %q", (*got)["description"], full)
		}
	})

	t.Run("jfrog config description takes the short tagline, never the readme", func(t *testing.T) {
		srv, got := capture()
		defer srv.Close()
		j := &JFrog{client: httpClient{base: srv.URL}, baseURL: srv.URL}
		if err := j.UpdateDescription(context.Background(), "docker-local/img", short, full); err != nil {
			t.Fatal(err)
		}
		if (*got)["description"] != short {
			t.Fatalf("jfrog description = %v, want the SHORT tagline %q (never the readme body)", (*got)["description"], short)
		}
	})
}
