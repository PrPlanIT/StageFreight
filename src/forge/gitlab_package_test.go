package forge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitLabPublishPackageFile pins the generic-package PUT: octet-stream body,
// the .../packages/generic/{pkg}/{ver}/{file} path, ?select=package_file, auth
// header, and the returned tokenless PullURL.
func TestGitLabPublishPackageFile(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "app-linux-amd64.tar.gz")
	if err := os.WriteFile(fp, []byte("PAYLOAD"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotMethod, gotEscPath, gotQuery, gotBody, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotEscPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("PRIVATE-TOKEN")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	g := &GitLabForge{BaseURL: srv.URL, Token: "secret", ProjectID: "grp/proj"}
	pub, err := g.PublishPackageFile(context.Background(), PublishPackageOptions{
		PackageName: "stagefreight",
		Version:     "dev-abc12345",
		FileName:    "app-linux-amd64.tar.gz",
		FilePath:    fp,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if gotMethod != "PUT" {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	wantPath := "/api/v4/projects/grp%2Fproj/packages/generic/stagefreight/dev-abc12345/app-linux-amd64.tar.gz"
	if gotEscPath != wantPath {
		t.Errorf("path = %s, want %s", gotEscPath, wantPath)
	}
	if gotQuery != "select=package_file" {
		t.Errorf("query = %s, want select=package_file", gotQuery)
	}
	if gotBody != "PAYLOAD" {
		t.Errorf("body = %q, want PAYLOAD", gotBody)
	}
	if gotAuth != "secret" {
		t.Errorf("auth = %q, want secret", gotAuth)
	}
	wantPull := srv.URL + wantPath
	if pub.PullURL != wantPull {
		t.Errorf("PullURL = %s, want %s", pub.PullURL, wantPull)
	}
}

// TestGitLabListPackageVersions confirms list parsing (id→ID, version, order).
func TestGitLabListPackageVersions(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
			{"id":10,"version":"dev-abc12345","created_at":"2024-01-02T03:04:05.000Z"},
			{"id":9,"version":"latest-dev","created_at":"2024-01-02T03:04:00.000Z"}
		]`))
	}))
	defer srv.Close()

	g := &GitLabForge{BaseURL: srv.URL, Token: "secret", ProjectID: "grp/proj"}
	versions, err := g.ListPackageVersions(context.Background(), "stagefreight")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(gotQuery, "package_type=generic") || !strings.Contains(gotQuery, "package_name=stagefreight") {
		t.Errorf("query missing filters: %s", gotQuery)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}
	if versions[0].ID != "10" || versions[0].Version != "dev-abc12345" {
		t.Errorf("v0 = %+v, want id=10 version=dev-abc12345", versions[0])
	}
	if versions[1].ID != "9" || versions[1].Version != "latest-dev" {
		t.Errorf("v1 = %+v, want id=9 version=latest-dev", versions[1])
	}
}

// TestGitLabDeletePackageVersion confirms version→id resolution then DELETE by id.
func TestGitLabDeletePackageVersion(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/packages"):
			_, _ = w.Write([]byte(`[
				{"id":10,"version":"dev-abc12345"},
				{"id":9,"version":"latest-dev"}
			]`))
		case r.Method == "DELETE":
			deletedPath = r.URL.EscapedPath()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	g := &GitLabForge{BaseURL: srv.URL, Token: "secret", ProjectID: "grp/proj"}
	if err := g.DeletePackageVersion(context.Background(), "stagefreight", "latest-dev"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.HasSuffix(deletedPath, "/packages/9") {
		t.Errorf("deleted path = %s, want suffix /packages/9", deletedPath)
	}

	// Deleting a version that doesn't exist is a no-op (idempotent prune).
	deletedPath = ""
	if err := g.DeletePackageVersion(context.Background(), "stagefreight", "nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
	if deletedPath != "" {
		t.Errorf("expected no DELETE for missing version, got %s", deletedPath)
	}
}
