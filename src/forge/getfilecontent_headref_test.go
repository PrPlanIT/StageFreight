package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These lock the empty-commit-churn fix across every forge: governance reconcile reads
// each file at ref "HEAD" to detect changes. A forge that passes "HEAD" straight to its
// content API and gets a 404 makes every file look new, so the reconcile writes identical
// content and the forge records a 0-file commit on every run. GetFileContent must resolve
// "HEAD" (like "") to the default branch and never leak the symbolic ref to the server.

func TestGitLabGetFileContent_ResolvesHEAD(t *testing.T) {
	var sawRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/repository/files/") {
			sawRef = r.URL.Query().Get("ref")
			if sawRef == "HEAD" { // GitLab does not serve the symbolic ref HEAD
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"content":"aGVsbG8=","encoding":"base64"}`)) // "hello"
			return
		}
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer srv.Close()

	g := &GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "grp/proj"}
	got, err := g.GetFileContent(context.Background(), ".stagefreight.yml", "HEAD")
	if err != nil {
		t.Fatalf("GetFileContent with HEAD: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	if sawRef != "main" {
		t.Fatalf("ref sent = %q, want resolved default branch %q (HEAD must not leak)", sawRef, "main")
	}
}

func TestGitHubGetFileContent_ResolvesHEAD(t *testing.T) {
	var sawRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/contents/") {
			sawRef = r.URL.Query().Get("ref")
			if sawRef == "HEAD" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"content":"aGVsbG8=","encoding":"base64"}`)) // "hello"
			return
		}
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer srv.Close()

	g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
	got, err := g.GetFileContent(context.Background(), ".stagefreight.yml", "HEAD")
	if err != nil {
		t.Fatalf("GetFileContent with HEAD: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	if sawRef != "main" {
		t.Fatalf("ref sent = %q, want resolved default branch %q (HEAD must not leak)", sawRef, "main")
	}
}

func TestForgejoGetFileContent_ResolvesHEAD(t *testing.T) {
	// Forgejo embeds *GiteaForge, so this also proves the Gitea fix and that Forgejo
	// inherits it.
	var sawRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/contents/") {
			sawRef = r.URL.Query().Get("ref")
			if sawRef == "HEAD" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"content":"aGVsbG8=","encoding":"base64"}`)) // "hello"
			return
		}
		_, _ = w.Write([]byte(`{"default_branch":"main"}`))
	}))
	defer srv.Close()

	f := &ForgejoForge{GiteaForge: &GiteaForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}}
	got, err := f.GetFileContent(context.Background(), ".stagefreight.yml", "HEAD")
	if err != nil {
		t.Fatalf("GetFileContent with HEAD: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	if sawRef != "main" {
		t.Fatalf("ref sent = %q, want resolved default branch %q (HEAD must not leak)", sawRef, "main")
	}
}

func TestAzureGetFileContent_ResolvesHEAD(t *testing.T) {
	var sawVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/items") {
			sawVersion = r.URL.Query().Get("versionDescriptor.version")
			if sawVersion == "HEAD" { // a branch literally named HEAD does not exist
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"content":"hello"}`))
			return
		}
		_, _ = w.Write([]byte(`{"defaultBranch":"refs/heads/main"}`))
	}))
	defer srv.Close()

	a := &AzureDevOpsForge{BaseURL: srv.URL, Project: "proj", Repo: "r", Token: "t"}
	got, err := a.GetFileContent(context.Background(), "/.stagefreight.yml", "HEAD")
	if err != nil {
		t.Fatalf("GetFileContent with HEAD: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	if sawVersion != "main" {
		t.Fatalf("version sent = %q, want resolved default branch %q (HEAD must not leak)", sawVersion, "main")
	}
}
