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
