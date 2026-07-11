package pages

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeWS(t *testing.T, ws string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(ws, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func wsFiles(t *testing.T, ws string) []string {
	t.Helper()
	var out []string
	filepath.Walk(ws, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(ws, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func TestFilterWorkspace_Exclude(t *testing.T) {
	ws := t.TempDir()
	writeWS(t, ws, "index.html", "assets/app.js", "assets/app.js.map", "drafts/wip.html")
	if err := FilterWorkspace(ws, DeployOpts{Exclude: []string{"**/*.map", "drafts/**"}}); err != nil {
		t.Fatal(err)
	}
	got := wsFiles(t, ws)
	want := []string{"assets/app.js", "index.html"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("after exclude = %v, want %v", got, want)
	}
}

func TestFilterWorkspace_IncludeAllowlist(t *testing.T) {
	ws := t.TempDir()
	writeWS(t, ws, "index.html", "downloads/a.pdf", "notes.txt")
	if err := FilterWorkspace(ws, DeployOpts{Include: []string{"downloads/**"}}); err != nil {
		t.Fatal(err)
	}
	got := wsFiles(t, ws)
	if len(got) != 1 || got[0] != "downloads/a.pdf" {
		t.Errorf("include allowlist = %v, want [downloads/a.pdf]", got)
	}
}

func TestGet_ProviderSelection(t *testing.T) {
	// Co-equal providers, no default — a name is required.
	if _, err := Get(""); err == nil {
		t.Error("empty provider should error (provider is required)")
	}
	if _, err := Get("cloudflare"); err != nil {
		t.Errorf("cloudflare: %v", err)
	}
	if _, err := Get("github"); err != nil {
		t.Errorf("github: %v", err)
	}
	if _, err := Get("bogus"); err == nil {
		t.Error("unknown provider should error")
	}
}

func TestGithubPrepare_MetadataAndFilter(t *testing.T) {
	ws := t.TempDir()
	writeWS(t, ws, "index.html", "x.map")
	g := &githubProvider{}
	if err := g.Prepare(ws, DeployOpts{Domains: []string{"docs.example.com"}, Exclude: []string{"*.map"}}); err != nil {
		t.Fatal(err)
	}
	// .nojekyll + CNAME written; the map filtered out.
	if _, err := os.Stat(filepath.Join(ws, ".nojekyll")); err != nil {
		t.Error(".nojekyll not written")
	}
	cname, err := os.ReadFile(filepath.Join(ws, "CNAME"))
	if err != nil || string(cname) != "docs.example.com\n" {
		t.Errorf("CNAME = %q (err %v), want the domain", string(cname), err)
	}
	if _, err := os.Stat(filepath.Join(ws, "x.map")); err == nil {
		t.Error("x.map should have been filtered out")
	}
}

func TestGithubPagesURL(t *testing.T) {
	if got := githubPagesURL("acme/docs", ""); got != "https://acme.github.io/docs/" {
		t.Errorf("project url = %q", got)
	}
	if got := githubPagesURL("acme/docs", "docs.example.com"); got != "https://docs.example.com/" {
		t.Errorf("custom-domain url = %q", got)
	}
}

// fakeProvider is a first-class test seam — it exercises the whole lifecycle
// (Prepare filters the workspace, Deploy observes the result) without any hosting.
type fakeProvider struct {
	preparedWS   string
	deployedWS   string
	deployedList []string
}

func (f *fakeProvider) Credentials() []CredentialRequirement { return nil }
func (f *fakeProvider) Prepare(ws string, opts DeployOpts) error {
	f.preparedWS = ws
	return FilterWorkspace(ws, opts)
}
func (f *fakeProvider) Deploy(_ context.Context, ws string, _ DeployOpts) (string, error) {
	f.deployedWS = ws
	f.deployedList = nil
	filepath.Walk(ws, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(ws, p)
			f.deployedList = append(f.deployedList, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(f.deployedList)
	return "https://fake.example", nil
}

func TestFakeProviderLifecycle(t *testing.T) {
	ws := t.TempDir()
	writeWS(t, ws, "index.html", "bundle.js.map")
	f := &fakeProvider{}
	if err := f.Prepare(ws, DeployOpts{Exclude: []string{"*.map"}}); err != nil {
		t.Fatal(err)
	}
	url, err := f.Deploy(context.Background(), ws, DeployOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if f.preparedWS != ws || f.deployedWS != ws {
		t.Errorf("lifecycle workspaces mismatch: prepared=%q deployed=%q", f.preparedWS, f.deployedWS)
	}
	if len(f.deployedList) != 1 || f.deployedList[0] != "index.html" {
		t.Errorf("deployed files = %v, want [index.html] (map excluded in Prepare)", f.deployedList)
	}
	if url == "" {
		t.Error("expected a deploy url")
	}
}
