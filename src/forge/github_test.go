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

// Re-publishing a tag must update the release that already exists, not fail. GitHub
// answers a POST for an existing tag with 422 already_exists, and the caller turned
// that into a hard error — so a re-pushed release never refreshed its notes, never
// uploaded its assets, and never moved Latest. That is how a repo keeps showing an
// old version as Latest while newer releases sit below it.
func TestGitHubCreateRelease_UpdatesExistingOnConflict(t *testing.T) {
	var patched string
	var patchBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/releases"):
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Release","code":"already_exists","field":"tag_name"}]}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/"):
			_, _ = w.Write([]byte(`{"id":4242,"html_url":"https://github.com/o/r/releases/tag/v1.2.3"}`))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/releases/"):
			patched = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&patchBody)
			_, _ = w.Write([]byte(`{"id":4242,"html_url":"https://github.com/o/r/releases/tag/v1.2.3"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	rel, err := g.CreateRelease(context.Background(), ReleaseOptions{
		TagName: "v1.2.3", Name: "v1.2.3", Description: "refreshed notes",
		Type: ReleaseTypeLatest, Ref: "abc123",
	})
	if err != nil {
		t.Fatalf("CreateRelease on existing tag: %v — a re-push must update, not fail", err)
	}
	if rel == nil || rel.ID != "4242" {
		t.Fatalf("release = %+v, want the existing release id 4242", rel)
	}
	if !strings.HasSuffix(patched, "/releases/4242") {
		t.Fatalf("patched path = %q, want /releases/4242", patched)
	}
	if patchBody["body"] != "refreshed notes" {
		t.Errorf("notes were not refreshed: body = %v", patchBody["body"])
	}
	if patchBody["make_latest"] != "true" {
		t.Errorf("make_latest = %v, want \"true\" — Latest must move to the re-published release", patchBody["make_latest"])
	}
	// tag_name and target_commitish are immutable on an existing release; sending them
	// is at best ignored and at worst rejected.
	if _, ok := patchBody["tag_name"]; ok {
		t.Error("update payload carried tag_name; it must not be re-sent")
	}
	if _, ok := patchBody["target_commitish"]; ok {
		t.Error("update payload carried target_commitish; it must not be re-sent")
	}
}

// A re-published (non-draft) release must have its published_at refreshed. GitHub keeps
// the ORIGINAL published_at across an in-place PATCH, so a re-cut would keep showing the
// first publish date. updateReleaseByTag toggles the release draft→published — a PATCH
// {draft:true} then the content PATCH {draft:false} — and that transition re-stamps
// published_at to now while keeping the release id, assets, and URL.
func TestGitHubUpdateRelease_RefreshesPublishedAtViaDraftToggle(t *testing.T) {
	var patchBodies []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/releases"):
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Release","code":"already_exists","field":"tag_name"}]}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/"):
			_, _ = w.Write([]byte(`{"id":4242,"html_url":"https://github.com/o/r/releases/tag/v1.2.3"}`))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/releases/"):
			var b map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&b)
			patchBodies = append(patchBodies, b)
			_, _ = w.Write([]byte(`{"id":4242,"html_url":"https://github.com/o/r/releases/tag/v1.2.3"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	if _, err := g.CreateRelease(context.Background(), ReleaseOptions{
		TagName: "v1.2.3", Name: "v1.2.3", Description: "notes", Type: ReleaseTypeLatest, Ref: "abc",
	}); err != nil {
		t.Fatalf("CreateRelease on existing tag: %v", err)
	}

	if len(patchBodies) != 2 {
		t.Fatalf("got %d PATCH requests, want 2 (draft:true then content draft:false)", len(patchBodies))
	}
	if patchBodies[0]["draft"] != true {
		t.Errorf("first PATCH draft = %v, want true (re-draft to refresh publish time)", patchBodies[0]["draft"])
	}
	if patchBodies[1]["draft"] != false {
		t.Errorf("second PATCH draft = %v, want false (re-publish)", patchBodies[1]["draft"])
	}
	if patchBodies[1]["body"] != "notes" {
		t.Errorf("second PATCH must carry the content: body = %v, want \"notes\"", patchBodies[1]["body"])
	}
}

// A 422 that is not an already-exists conflict must still fail. Swallowing every
// validation error would hide real misconfiguration behind a lookup that then 404s.
func TestGitHubCreateRelease_OtherValidationErrorStillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"resource":"Release","code":"invalid","field":"tag_name"}]}`))
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	if _, err := g.CreateRelease(context.Background(), ReleaseOptions{TagName: "bad tag"}); err == nil {
		t.Fatal("CreateRelease returned nil error for a non-conflict 422")
	}
}

// UpdateRelease is the mirror-convergence path (its only caller). It must re-stamp
// published_at via the draft-toggle — GitHub keeps the original across a plain PATCH, so
// without this a re-cut whose notes changed kept showing the first publish date.
func TestGitHubUpdateRelease_ReStampsViaDraftToggle(t *testing.T) {
	var patchBodies []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/releases/") {
			var b map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&b)
			patchBodies = append(patchBodies, b)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	if err := g.UpdateRelease(context.Background(), "4242",
		ReleaseMeta{Name: "v1.2.3", Description: "notes", Type: ReleaseTypeLatest}); err != nil {
		t.Fatalf("UpdateRelease: %v", err)
	}
	if len(patchBodies) != 2 {
		t.Fatalf("got %d PATCHes, want 2 (draft:true then content draft:false)", len(patchBodies))
	}
	if patchBodies[0]["draft"] != true {
		t.Errorf("first PATCH draft = %v, want true (re-draft to refresh publish time)", patchBodies[0]["draft"])
	}
	if patchBodies[1]["draft"] != false || patchBodies[1]["body"] != "notes" {
		t.Errorf("second PATCH = %v, want the content with draft:false", patchBodies[1])
	}
}
