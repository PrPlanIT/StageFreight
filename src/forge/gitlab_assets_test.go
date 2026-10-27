package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// fakeUploadsServer records every request ("METHOD escaped-path") and answers
// from routes; an unrouted request gets 404, which the upload delete treats as
// already gone.
func fakeUploadsServer(t *testing.T, routes map[string]func(w http.ResponseWriter)) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		calls = append(calls, key)
		if h, ok := routes[key]; ok {
			h(w)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// Deleting a release must also delete the uploads its links point at: GitLab keeps
// an uploaded file after its release is gone, so a pruned rolling release would
// otherwise leave its assets stored forever. Links that are not this project's
// uploads (an image reference) must be left alone.
func TestGitLab_DeleteRelease_DeletesLinkedUploads(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	var base string
	routes := map[string]func(w http.ResponseWriter){
		"GET /api/v4/projects/grp%2Fproj/releases/dev-abc": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{"assets":{"links":[
				{"url":"%s/grp/proj/uploads/%s/app%%2Bdev.zip"},
				{"url":"https://quay.io/repository/prplanit/stagefreight"}
			]}}`, base, secret)
		},
		"DELETE /api/v4/projects/grp%2Fproj/releases/dev-abc":                   func(w http.ResponseWriter) {},
		"DELETE /api/v4/projects/grp%2Fproj/uploads/" + secret + "/app+dev.zip": func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
	}
	srv, calls := fakeUploadsServer(t, routes)
	base = srv.URL

	g := &GitLabForge{BaseURL: srv.URL, ProjectID: "grp/proj", Token: "x"}
	if err := g.DeleteRelease(context.Background(), "dev-abc"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /api/v4/projects/grp%2Fproj/releases/dev-abc",
		"DELETE /api/v4/projects/grp%2Fproj/releases/dev-abc",
		"DELETE /api/v4/projects/grp%2Fproj/uploads/" + secret + "/app+dev.zip",
	}
	if fmt.Sprint(*calls) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}
}

// Replacing one asset (the mirror's drift path) must delete the link's upload too.
func TestGitLab_DeleteReleaseAsset_DeletesUpload(t *testing.T) {
	const secret = "fedcba9876543210fedcba9876543210"
	var base string
	routes := map[string]func(w http.ResponseWriter){
		"GET /api/v4/projects/grp%2Fproj/releases/v1.0.0/assets/links/7": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{"url":"%s/grp/proj/uploads/%s/app.zip"}`, base, secret)
		},
		"DELETE /api/v4/projects/grp%2Fproj/releases/v1.0.0/assets/links/7": func(w http.ResponseWriter) {},
		"DELETE /api/v4/projects/grp%2Fproj/uploads/" + secret + "/app.zip": func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) },
	}
	srv, calls := fakeUploadsServer(t, routes)
	base = srv.URL

	g := &GitLabForge{BaseURL: srv.URL, ProjectID: "grp/proj", Token: "x"}
	if err := g.DeleteReleaseAsset(context.Background(), "v1.0.0", "7"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 || (*calls)[2] != "DELETE /api/v4/projects/grp%2Fproj/uploads/"+secret+"/app.zip" {
		t.Errorf("upload not deleted after its link: calls = %v", *calls)
	}
}

// An upload that is already gone (404) is success; any other failure surfaces and
// names the file, so a stored file is never orphaned silently.
func TestGitLab_DeleteRelease_UploadFailureSurfaces(t *testing.T) {
	const gone, stuck = "11111111111111111111111111111111", "22222222222222222222222222222222"
	var base string
	routes := map[string]func(w http.ResponseWriter){
		"GET /api/v4/projects/grp%2Fproj/releases/dev-x": func(w http.ResponseWriter) {
			fmt.Fprintf(w, `{"assets":{"links":[{"url":"%s/grp/proj/uploads/%s/a.zip"},{"url":"%s/grp/proj/uploads/%s/b.zip"}]}}`,
				base, gone, base, stuck)
		},
		"DELETE /api/v4/projects/grp%2Fproj/releases/dev-x":              func(w http.ResponseWriter) {},
		"DELETE /api/v4/projects/grp%2Fproj/uploads/" + stuck + "/b.zip": func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) },
	}
	srv, _ := fakeUploadsServer(t, routes)
	base = srv.URL

	g := &GitLabForge{BaseURL: srv.URL, ProjectID: "grp/proj", Token: "x"}
	err := g.DeleteRelease(context.Background(), "dev-x")
	if err == nil {
		t.Fatal("a 403 deleting an upload must surface")
	}
	if !strings.Contains(err.Error(), stuck+"/b.zip") || strings.Contains(err.Error(), gone) {
		t.Errorf("error should name only the stuck file: %v", err)
	}
}

func TestGitLab_UploadRef(t *testing.T) {
	g := &GitLabForge{BaseURL: "https://gitlab.example.com", ProjectID: "grp/proj"}
	const secret = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		url      string
		ok       bool
		filename string
	}{
		{"https://gitlab.example.com/grp/proj/uploads/" + secret + "/app.zip", true, "app.zip"},
		{"https://gitlab.example.com/grp/sub/proj/uploads/" + secret + "/a%2Bb.tar.gz", true, "a+b.tar.gz"},
		{"https://gitlab.example.com/grp/proj/-/releases/v1/downloads/app.zip", false, ""},
		{"https://gitlab.example.com/api/v4/projects/1/packages/generic/app/1.0/app.zip", false, ""},
		{"https://other.example.com/grp/proj/uploads/" + secret + "/app.zip", false, ""},
		{"https://gitlab.example.com/grp/proj/uploads/not-a-secret/app.zip", false, ""},
	}
	for _, c := range cases {
		s, f, ok := g.uploadRef(c.url)
		if ok != c.ok || (ok && (s != secret || f != c.filename)) {
			t.Errorf("uploadRef(%q) = %q, %q, %v; want ok=%v filename=%q", c.url, s, f, ok, c.ok, c.filename)
		}
	}
}
