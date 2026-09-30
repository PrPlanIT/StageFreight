package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Gitea/Forgejo answer a duplicate-tag release POST with 409 Conflict. Before, the caller
// returned that as a hard error — so a re-cut never refreshed notes, uploaded assets, or
// moved the publish date; it just failed. CreateRelease must now treat 409 as a re-publish:
// resolve the existing release and update it in place, re-stamping the date via a
// draft-toggle (PATCH draft:true then the content PATCH draft:false).
func TestGiteaCreateRelease_UpdatesExistingOnConflict(t *testing.T) {
	var patchBodies []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/releases"):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"release already exists"}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/"):
			_, _ = w.Write([]byte(`{"id":7,"html_url":"http://x/releases/tag/v1.2.3"}`))
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/releases/"):
			var b map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&b)
			patchBodies = append(patchBodies, b)
			_, _ = w.Write([]byte(`{"id":7,"html_url":"http://x/releases/tag/v1.2.3"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	g := &GiteaForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	rel, err := g.CreateRelease(context.Background(), ReleaseOptions{
		TagName: "v1.2.3", Name: "v1.2.3", Description: "notes", Type: ReleaseTypeLatest, Ref: "abc",
	})
	if err != nil {
		t.Fatalf("CreateRelease on existing tag: %v — a re-cut must update, not fail on 409", err)
	}
	if rel == nil || rel.ID != "7" {
		t.Fatalf("release = %+v, want the existing release id 7", rel)
	}
	if len(patchBodies) != 2 {
		t.Fatalf("got %d PATCHes, want 2 (draft:true then content draft:false)", len(patchBodies))
	}
	if patchBodies[0]["draft"] != true {
		t.Errorf("first PATCH draft = %v, want true (re-draft to refresh publish time)", patchBodies[0]["draft"])
	}
	if patchBodies[1]["draft"] != false {
		t.Errorf("second PATCH draft = %v, want false (re-publish)", patchBodies[1]["draft"])
	}
	if patchBodies[1]["body"] != "notes" {
		t.Errorf("content PATCH must carry the notes: body = %v", patchBodies[1]["body"])
	}
	// tag_name / target_commitish are immutable once a release exists.
	if _, ok := patchBodies[1]["tag_name"]; ok {
		t.Error("update payload carried tag_name; it must be dropped")
	}
	if _, ok := patchBodies[1]["target_commitish"]; ok {
		t.Error("update payload carried target_commitish; it must be dropped")
	}
}

// A non-conflict error from the create POST must still surface (not be swallowed as a
// re-publish, which would only 404 on the lookup and bury the real cause).
func TestGiteaCreateRelease_NonConflictErrorStillFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"invalid target"}`))
	}))
	defer srv.Close()

	g := &GiteaForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	if _, err := g.CreateRelease(context.Background(), ReleaseOptions{TagName: "v1.2.3", Ref: "abc"}); err == nil {
		t.Fatal("expected the 422 to surface, not be treated as a conflict")
	}
}
