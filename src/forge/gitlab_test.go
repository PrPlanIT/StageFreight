package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestGitLabUploadAsset_UsesFullPath locks the release-download fix: GitLab's /uploads
// returns a PROJECT-RELATIVE `url` (/uploads/...) plus a resolvable `full_path`
// (/<namespace>/<project>/uploads/...). The asset link must point at full_path, else the
// /-/releases/<tag>/downloads/<name> permalink redirects to a namespace-less 404.
func TestGitLabUploadAsset_UsesFullPath(t *testing.T) {
	var linkURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/uploads"):
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"url":"/uploads/abc/app.tar.gz","full_path":"/grp/proj/uploads/abc/app.tar.gz","markdown":"x"}`))
		case strings.HasSuffix(r.URL.Path, "/assets/links"):
			b, _ := io.ReadAll(r.Body)
			m := map[string]string{}
			_ = json.Unmarshal(b, &m)
			linkURL = m["url"]
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	f, err := os.CreateTemp(t.TempDir(), "app-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("x")
	_ = f.Close()

	g := &GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "grp/proj"}
	if err := g.UploadAsset(context.Background(), "latest-dev", Asset{Name: "app.tar.gz", FilePath: f.Name()}); err != nil {
		t.Fatalf("UploadAsset: %v", err)
	}
	want := srv.URL + "/grp/proj/uploads/abc/app.tar.gz"
	if linkURL != want {
		t.Fatalf("asset link url = %q, want %q (must use full_path, not the namespace-less /uploads url)", linkURL, want)
	}
}

// TestGitLabAddReleaseLink_DirectAssetPath verifies the asset-link payload carries
// direct_asset_path (which yields a permanent /-/releases/<tag>/downloads/<path>
// permalink) when set, and omits the key entirely when unset — so non-channel
// links (e.g. registry image links) are unaffected.
func TestGitLabAddReleaseLink_DirectAssetPath(t *testing.T) {
	var captured map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/assets/links") {
			w.WriteHeader(http.StatusOK)
			return
		}
		b, _ := io.ReadAll(r.Body)
		captured = map[string]string{}
		_ = json.Unmarshal(b, &captured)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	g := &GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "grp/proj"}

	// With DirectAssetPath → present.
	if err := g.AddReleaseLink(context.Background(), "latest-dev", ReleaseLink{
		Name: "dwiz", URL: srv.URL + "/uploads/x/dwiz.zip", LinkType: "other",
		DirectAssetPath: "/dwiz-windows-amd64.zip",
	}); err != nil {
		t.Fatalf("AddReleaseLink: %v", err)
	}
	if got := captured["direct_asset_path"]; got != "/dwiz-windows-amd64.zip" {
		t.Errorf("direct_asset_path = %q, want /dwiz-windows-amd64.zip", got)
	}

	// Without it → key absent.
	captured = nil
	if err := g.AddReleaseLink(context.Background(), "latest-dev", ReleaseLink{
		Name: "img", URL: "https://hub/img", LinkType: "image",
	}); err != nil {
		t.Fatalf("AddReleaseLink (no path): %v", err)
	}
	if _, ok := captured["direct_asset_path"]; ok {
		t.Errorf("direct_asset_path must be absent when unset, got %q", captured["direct_asset_path"])
	}
}

// TestGitLabAddReleaseLink_Idempotent proves AddReleaseLink reconciles against an existing
// link of the same name instead of blindly POSTing a duplicate — GitLab rejects that with
// 400 "Name has already been taken", which surfaced as a false failure when a rolling channel
// (dev-<sha>, latest-dev) was re-published. Same URL → no-op; drifted URL → in-place PUT.
func TestGitLabAddReleaseLink_Idempotent(t *testing.T) {
	var posted, putPath string
	existing := `[{"id":7,"name":"Docker Hub v1","url":"https://hub/img"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/assets/links"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(existing))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/assets/links"):
			posted = "called"
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":["Name has already been taken"]}`))
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/assets/links/"):
			putPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	g := &GitLabForge{BaseURL: srv.URL, Token: "t", ProjectID: "grp/proj"}

	// Same name + same URL → no-op: neither POST nor PUT.
	if err := g.AddReleaseLink(context.Background(), "dev-abc", ReleaseLink{
		Name: "Docker Hub v1", URL: "https://hub/img", LinkType: "image",
	}); err != nil {
		t.Fatalf("idempotent no-op: %v", err)
	}
	if posted != "" || putPath != "" {
		t.Fatalf("identical link must be a no-op; posted=%q put=%q", posted, putPath)
	}

	// Same name + drifted URL → PUT the existing link id, never POST.
	if err := g.AddReleaseLink(context.Background(), "dev-abc", ReleaseLink{
		Name: "Docker Hub v1", URL: "https://hub/img@sha256:new", LinkType: "image",
	}); err != nil {
		t.Fatalf("idempotent update: %v", err)
	}
	if posted != "" {
		t.Fatalf("drifted link must PUT not POST; posted=%q", posted)
	}
	if !strings.HasSuffix(putPath, "/assets/links/7") {
		t.Fatalf("expected PUT to link id 7, got %q", putPath)
	}
}

// gitlabDirectAssetPath must produce a path GitLab accepts: leading slash and
// only [A-Za-z0-9._-]. The SemVer build-metadata '+' (e.g. "0.6.1-dev+6e376f2")
// previously leaked through and GitLab rejected the link with
// "Filepath is in an invalid format".
func TestGitLabDirectAssetPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"stagefreight-0.6.1-dev+6e376f2-linux-amd64.tar.gz", "/stagefreight-0.6.1-dev-6e376f2-linux-amd64.tar.gz"},
		{"app-1.0.0.tar.gz", "/app-1.0.0.tar.gz"}, // already valid, unchanged
		{"weird name (v2)+x.bin", "/weird-name--v2--x.bin"},
	}
	for _, c := range cases {
		got := gitlabDirectAssetPath(c.in)
		if got != c.want {
			t.Errorf("gitlabDirectAssetPath(%q) = %q, want %q", c.in, got, c.want)
		}
		if got == "" || got[0] != '/' {
			t.Errorf("gitlabDirectAssetPath(%q) must start with '/', got %q", c.in, got)
		}
		for _, r := range got[1:] {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
				r == '.' || r == '-' || r == '_'
			if !ok {
				t.Errorf("gitlabDirectAssetPath(%q) = %q contains GitLab-invalid rune %q", c.in, got, r)
			}
		}
	}
}
