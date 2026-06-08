package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
