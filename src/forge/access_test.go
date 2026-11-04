package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveRepo answers GET <path> with body and fails the test on anything else.
func serveRepo(t *testing.T, path, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != path {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAccess_GitHub(t *testing.T) {
	for body, want := range map[string]AccessLevel{
		`{"permissions":{"admin":true,"maintain":true,"push":true,"triage":true,"pull":true}}`:    AccessAdmin,
		`{"permissions":{"admin":false,"maintain":true,"push":true,"triage":true,"pull":true}}`:   AccessMaintain,
		`{"permissions":{"admin":false,"maintain":false,"push":true,"triage":true,"pull":true}}`:  AccessWrite,
		`{"permissions":{"admin":false,"maintain":false,"push":false,"triage":true,"pull":true}}`: AccessRead,
		`{}`: AccessNone,
	} {
		srv := serveRepo(t, "/repos/o/r", body)
		g := &GitHubForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
		if got, err := g.Access(context.Background()); err != nil || got != want {
			t.Errorf("%s: Access = %d, %v; want %d", body, got, err, want)
		}
	}
}

func TestAccess_GitLab(t *testing.T) {
	for body, want := range map[string]AccessLevel{
		`{"permissions":{"project_access":{"access_level":50},"group_access":null}}`:                AccessAdmin,
		`{"permissions":{"project_access":null,"group_access":{"access_level":40}}}`:                AccessMaintain,
		`{"permissions":{"project_access":{"access_level":30},"group_access":{"access_level":40}}}`: AccessMaintain, // higher wins
		`{"permissions":{"project_access":{"access_level":30},"group_access":null}}`:                AccessWrite,
		`{"permissions":{"project_access":{"access_level":20},"group_access":null}}`:                AccessRead,
		`{"permissions":{"project_access":null,"group_access":null}}`:                               AccessNone,
	} {
		srv := serveRepo(t, "/api/v4/projects/g/p", body)
		g := &GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "g/p"}
		if got, err := g.Access(context.Background()); err != nil || got != want {
			t.Errorf("%s: Access = %d, %v; want %d", body, got, err, want)
		}
	}
}

func TestAccess_Gitea(t *testing.T) {
	for body, want := range map[string]AccessLevel{
		`{"permissions":{"admin":true,"push":true,"pull":true}}`:   AccessAdmin,
		`{"permissions":{"admin":false,"push":true,"pull":true}}`:  AccessWrite,
		`{"permissions":{"admin":false,"push":false,"pull":true}}`: AccessRead,
	} {
		srv := serveRepo(t, "/api/v1/repos/o/r", body)
		g := &GiteaForge{BaseURL: srv.URL, Token: "t", Owner: "o", Repo: "r"}
		if got, err := g.Access(context.Background()); err != nil || got != want {
			t.Errorf("%s: Access = %d, %v; want %d", body, got, err, want)
		}
	}
	// Forgejo inherits Gitea's.
	var _ AccessReader = NewForgejo("https://example.com")
}
