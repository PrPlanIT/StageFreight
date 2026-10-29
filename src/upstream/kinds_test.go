package upstream

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream/scheme"
)

func gz(t *testing.T, body []byte) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(body)
	w.Close()
	return b.Bytes()
}

func serve(t *testing.T, routes map[string][]byte) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body, ok := routes[r.URL.Path]; ok {
			w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func namesOf(c []Candidate) string {
	var out []string
	for _, x := range c {
		out = append(out, x.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestListers(t *testing.T) {
	var apkIndex bytes.Buffer
	tw := tar.NewWriter(&apkIndex)
	idx := []byte("P:nut\nV:2.8.3-r0\nt:1750000000\n\nP:musl\nV:1.2.5-r10\n\n")
	tw.WriteHeader(&tar.Header{Name: "APKINDEX", Mode: 0o644, Size: int64(len(idx))})
	tw.Write(idx)
	tw.Close()

	srv := serve(t, map[string][]byte{
		"/pypi/ansible-core/json":                      []byte(`{"releases":{"2.21.5":[{"upload_time_iso_8601":"2026-09-30T12:00:00Z"}],"2.21.4":[{"upload_time_iso_8601":"2026-08-01T00:00:00Z","yanked":true}],"2.0":[]}}`),
		"/npm/@scope/pkg":                              []byte(`{"versions":{"1.2.0":{},"1.3.0":{"deprecated":"broken"}},"time":{"1.2.0":"2026-01-01T00:00:00Z"}}`),
		"/gh/o/r/releases":                             []byte(`[{"tag_name":"v0.9.0","published_at":"2026-05-01T00:00:00Z"},{"tag_name":"v1.0.0-beta.1","prerelease":true},{"tag_name":"v1.0.0","draft":true}]`),
		"/alpine/v3.22/main/x86_64/APKINDEX.tar.gz":    gz(t, apkIndex.Bytes()),
		"/debian/trixie/main/binary-amd64/Packages.gz": gz(t, []byte("Package: apt-cacher-ng\nVersion: 3.7.5-1\n\nPackage: nut\nVersion: 2.8.1-3\n\n")),
		"/download.html":                               []byte(`<a href="nut-2.8.2.tar.gz">2.8.2</a> <a href="nut-2.8.3.tar.gz">2.8.3</a> <a href="nut-2.8.3.tar.gz.sha256">sum</a>`),
	})
	defer func(a, b, c, d, e string) { pypiBase, npmBase, githubAPI, alpineBase, debianBase = a, b, c, d, e }(pypiBase, npmBase, githubAPI, alpineBase, debianBase)
	pypiBase, npmBase, githubAPI, alpineBase, debianBase = srv.URL+"/pypi/", srv.URL+"/npm/", srv.URL+"/gh/", srv.URL+"/alpine/", srv.URL+"/debian/"

	ctx := t.Context()
	cases := []struct {
		src  Source
		want string
	}{
		{Source{Kind: config.UpstreamKindPyPI, Location: "ansible-core"}, "2.21.5"},               // yanked and file-less versions dropped
		{Source{Kind: config.UpstreamKindNpm, Location: "@scope/pkg"}, "1.2.0"},                   // deprecated dropped
		{Source{Kind: config.UpstreamKindGitHubRelease, Location: "o/r"}, "v0.9.0,v1.0.0-beta.1"}, // draft dropped
		{Source{Kind: config.UpstreamKindApk, Location: "nut", Suite: "v3.22/main"}, "2.8.3-r0"},
		{Source{Kind: config.UpstreamKindApt, Location: "apt-cacher-ng", Suite: "trixie/main"}, "3.7.5-1"},
		{Source{Kind: config.UpstreamKindURL, Location: srv.URL + "/download.html", Select: Selection{Tags: regexp.MustCompile(`nut-(?P<version>[\d.]+)\.tar\.gz\b`)}}, "nut-2.8.2.tar.gz,nut-2.8.3.tar.gz"},
	}
	for _, c := range cases {
		got, err := List(ctx, c.src)
		if err != nil || namesOf(got) != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.src.Kind, namesOf(got), err, c.want)
		}
	}

	// Publish times flow onto releases, and GitHub's prerelease flag is honoured.
	gh, _ := List(ctx, Source{Kind: config.UpstreamKindGitHubRelease, Location: "o/r"})
	sem, _ := scheme.Get(scheme.Semver)
	rels, _ := ReleasesOf(gh, Selection{Tags: regexp.MustCompile(config.DefaultUpstreamTags), Scheme: sem})
	if len(rels) != 1 || rels[0].Tag != "v0.9.0" || rels[0].Published.IsZero() {
		t.Fatalf("releases = %+v", rels)
	}
	apk, _ := List(ctx, Source{Kind: config.UpstreamKindApk, Location: "nut", Suite: "v3.22/main"})
	if apk[0].Published.Unix() != 1750000000 {
		t.Errorf("apk build time = %v", apk[0].Published)
	}
	if _, err := List(ctx, Source{Kind: config.UpstreamKindApt, Location: "x", Suite: "trixie"}); err == nil {
		t.Error("a suite without a repository is refused")
	}
}
