package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// GitLab release links must be CLASSIFIED, not collapsed: a direct-asset URL hosted on the
// GitLab host is a downloadable FILE; an image/registry link (or a foreign-host URL) is an
// EXTERNAL reference to re-create as a link, never downloaded.
func TestGitLab_ListReleaseAssets_ClassifiesFileVsExternalLink(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/grp%2Fproj/releases/v0.0.5", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"assets":{"links":[
			{"id":1,"name":"binary.zip","url":"%s/grp/proj/-/releases/v0.0.5/downloads/binary.zip","direct_asset_url":"%s/grp/proj/-/releases/v0.0.5/downloads/binary.zip","link_type":"other"},
			{"id":2,"name":"quay v0.0.5","url":"https://quay.io/repository/prplanit/stagefreight","direct_asset_url":"","link_type":"image"}
		]}}`, srvURL, srvURL)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	g := &GitLabForge{BaseURL: srv.URL, ProjectID: "grp/proj", Token: "x"}
	assets, err := g.ListReleaseAssets(context.Background(), "v0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ReleaseAsset{}
	for _, a := range assets {
		byName[a.Name] = a
	}

	file := byName["binary.zip"]
	if file.External {
		t.Errorf("direct-asset on the GitLab host must be a FILE (External=false), got %+v", file)
	}
	if file.URL == "" || file.URL == "https://quay.io/repository/prplanit/stagefreight" {
		t.Errorf("file URL should be the direct-asset download URL, got %q", file.URL)
	}

	link := byName["quay v0.0.5"]
	if !link.External {
		t.Errorf("an image link must be EXTERNAL (never downloaded), got %+v", link)
	}
	if link.URL != "https://quay.io/repository/prplanit/stagefreight" {
		t.Errorf("external link URL = %q, want the registry reference", link.URL)
	}
	if link.LinkType != "image" {
		t.Errorf("link_type = %q, want image", link.LinkType)
	}
}
