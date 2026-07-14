package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHubCreateRelease_LowersReleaseType pins the intent→native mapping: Latest sends
// make_latest="true", Prerelease sends prerelease=true and no make_latest, and Auto sends
// neither make_latest (preserving GitHub's default) nor prerelease=true.
func TestGitHubCreateRelease_LowersReleaseType(t *testing.T) {
	cases := []struct {
		name           string
		typ            ReleaseType
		wantPrerelease bool
		wantMakeLatest string // "" means the field must be ABSENT
	}{
		{"latest", ReleaseTypeLatest, false, "true"},
		{"prerelease", ReleaseTypePrerelease, true, ""},
		{"auto", ReleaseTypeAuto, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/releases") {
					_ = json.NewDecoder(r.Body).Decode(&body)
					_, _ = w.Write([]byte(`{"id":1,"html_url":"http://x"}`))
					return
				}
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}))
			defer srv.Close()

			g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
			if _, err := g.CreateRelease(context.Background(), ReleaseOptions{TagName: "v1", Type: tc.typ}); err != nil {
				t.Fatalf("CreateRelease: %v", err)
			}
			if got, _ := body["prerelease"].(bool); got != tc.wantPrerelease {
				t.Errorf("prerelease = %v, want %v", body["prerelease"], tc.wantPrerelease)
			}
			ml, present := body["make_latest"]
			if tc.wantMakeLatest == "" && present {
				t.Errorf("make_latest = %v present, want absent", ml)
			}
			if tc.wantMakeLatest != "" && ml != tc.wantMakeLatest {
				t.Errorf("make_latest = %v, want %q", ml, tc.wantMakeLatest)
			}
		})
	}
}

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
