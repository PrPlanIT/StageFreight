package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHubDeleteRelease_ReapsDraft locks the retention fix: a release whose tag was
// pruned (e.g. on a mirror) becomes a GitHub DRAFT, and GET /releases/tags/{tag} 404s
// for drafts. DeleteRelease must fall back to the list endpoint (which includes drafts,
// still carrying their tag_name) and delete by ID — otherwise drafts pile up forever.
func TestGitHubDeleteRelease_ReapsDraft(t *testing.T) {
	var deletedPath string
	var listed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/releases/"):
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/"):
			// Drafts have no tag ref — this endpoint 404s for them.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases"):
			listed = true
			_, _ = w.Write([]byte(`[{"id":555,"tag_name":"dev-abc","draft":true,"created_at":"2026-01-01T00:00:00Z"}]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	if err := g.DeleteRelease(context.Background(), "dev-abc"); err != nil {
		t.Fatalf("DeleteRelease(draft): %v — a drafted release must be reaped, not error out", err)
	}
	if !listed {
		t.Error("list endpoint was not consulted — the by-tag 404 fallback did not run")
	}
	if !strings.HasSuffix(deletedPath, "/releases/555") {
		t.Errorf("deleted path = %q, want the draft's numeric id (/releases/555)", deletedPath)
	}
}

// TestGitHubDeleteRelease_PublishedFastPath confirms the common case is unchanged: a
// published release resolves via GET /releases/tags/{tag} and is deleted by that id,
// with no fallback list call.
func TestGitHubDeleteRelease_PublishedFastPath(t *testing.T) {
	var deletedPath string
	var listed bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/releases/"):
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/"):
			_, _ = w.Write([]byte(`{"id":42,"tag_name":"v1.2.3"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases"):
			listed = true
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	if err := g.DeleteRelease(context.Background(), "v1.2.3"); err != nil {
		t.Fatalf("DeleteRelease(published): %v", err)
	}
	if listed {
		t.Error("list endpoint was consulted for a published release — fast path should not fall back")
	}
	if !strings.HasSuffix(deletedPath, "/releases/42") {
		t.Errorf("deleted path = %q, want /releases/42", deletedPath)
	}
}
