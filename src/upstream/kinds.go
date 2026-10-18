package upstream

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Candidate is one thing an upstream publishes that might be a release: its name (a tag, or a
// version for registries), and whatever the kind knows about it.
type Candidate struct {
	Name       string
	Commit     string    // git: the commit the tag names (annotated tags peeled)
	Published  time.Time // registries that publish a release time; zero when unknown
	Prerelease bool      // the upstream itself marks it a prerelease (GitHub)
}

// Registry endpoints; variables so tests can stand in for them.
var (
	pypiBase   = "https://pypi.org/pypi/"
	npmBase    = "https://registry.npmjs.org/"
	githubAPI  = "https://api.github.com/repos/"
	alpineBase = "https://dl-cdn.alpinelinux.org/alpine/"
	debianBase = "https://deb.debian.org/debian/dists/"
)

// maxIndex bounds a downloaded index or page.
const maxIndex = 256 << 20

// List lists what the source publishes. Listing is anonymous: an upstream is somebody else's
// public project (github-release uses GITHUB_TOKEN when the environment has one, for the API's
// rate limit).
func List(ctx context.Context, src Source) ([]Candidate, error) {
	switch src.Kind {
	case config.UpstreamKindGit:
		return listGit(ctx, src.Location)
	case config.UpstreamKindImage:
		repo, err := name.NewRepository(src.Location)
		if err != nil {
			return nil, fmt.Errorf("image %s: %w", src.Location, err)
		}
		tags, err := remote.List(repo, remote.WithContext(ctx), remote.WithAuth(authn.Anonymous))
		if err != nil {
			return nil, fmt.Errorf("listing tags of %s: %w", src.Location, err)
		}
		return names(tags), nil
	case config.UpstreamKindPyPI:
		return listPyPI(ctx, src.Location)
	case config.UpstreamKindNpm:
		return listNpm(ctx, src.Location)
	case config.UpstreamKindGitHubRelease:
		return listGitHubReleases(ctx, src.Location)
	case config.UpstreamKindApk:
		return listApk(ctx, src.Location, src.Suite)
	case config.UpstreamKindApt:
		return listApt(ctx, src.Location, src.Suite)
	case config.UpstreamKindURL:
		return listURL(ctx, src)
	}
	return nil, fmt.Errorf("unknown upstream kind %q", src.Kind)
}

func names(ss []string) []Candidate {
	out := make([]Candidate, 0, len(ss))
	for _, s := range ss {
		out = append(out, Candidate{Name: s})
	}
	return out
}

func listGit(ctx context.Context, location string) ([]Candidate, error) {
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "upstream", URLs: []string{location}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{PeelingOption: git.AppendPeeled})
	if err != nil {
		return nil, fmt.Errorf("ls-remote %s: %w", location, err)
	}
	commits := map[string]string{}
	var tags []string
	for _, r := range refs {
		if !r.Name().IsTag() {
			continue
		}
		n := r.Name().Short()
		if base, peeled := strings.CutSuffix(n, "^{}"); peeled {
			commits[base] = r.Hash().String() // the commit an annotated tag points at
			continue
		}
		tags = append(tags, n)
		if _, ok := commits[n]; !ok {
			commits[n] = r.Hash().String()
		}
	}
	out := names(tags)
	for i := range out {
		out[i].Commit = commits[out[i].Name]
	}
	return out, nil
}

// get fetches a URL with the run's context, failing on a non-2xx answer.
func get(ctx context.Context, u string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "StageFreight (upstream watch)")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return resp, nil
}

func getJSON(ctx context.Context, u string, header map[string]string, into any) error {
	resp, err := get(ctx, u, header)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxIndex)).Decode(into)
}

func listPyPI(ctx context.Context, project string) ([]Candidate, error) {
	var doc struct {
		Releases map[string][]struct {
			Uploaded string `json:"upload_time_iso_8601"`
			Yanked   bool   `json:"yanked"`
		} `json:"releases"`
	}
	if err := getJSON(ctx, pypiBase+url.PathEscape(project)+"/json", nil, &doc); err != nil {
		return nil, fmt.Errorf("pypi %s: %w", project, err)
	}
	var out []Candidate
	for v, files := range doc.Releases {
		if len(files) == 0 {
			continue // a version with no files was never installable
		}
		c := Candidate{Name: v}
		yanked := true
		for _, f := range files {
			yanked = yanked && f.Yanked
			if t, err := time.Parse(time.RFC3339, f.Uploaded); err == nil && (c.Published.IsZero() || t.Before(c.Published)) {
				c.Published = t
			}
		}
		if !yanked {
			out = append(out, c)
		}
	}
	return out, nil
}

func listNpm(ctx context.Context, pkg string) ([]Candidate, error) {
	var doc struct {
		Versions map[string]struct {
			Deprecated any `json:"deprecated"`
		} `json:"versions"`
		Time map[string]string `json:"time"`
	}
	escaped := strings.Replace(url.PathEscape(pkg), "%40", "@", 1)
	if err := getJSON(ctx, npmBase+escaped, map[string]string{"Accept": "application/json"}, &doc); err != nil {
		return nil, fmt.Errorf("npm %s: %w", pkg, err)
	}
	var out []Candidate
	for v, meta := range doc.Versions {
		if meta.Deprecated != nil && meta.Deprecated != false && meta.Deprecated != "" {
			continue
		}
		c := Candidate{Name: v}
		if t, err := time.Parse(time.RFC3339, doc.Time[v]); err == nil {
			c.Published = t
		}
		out = append(out, c)
	}
	return out, nil
}

// githubPages bounds how far back the release list is read (100 per page).
const githubPages = 5

func listGitHubReleases(ctx context.Context, repo string) ([]Candidate, error) {
	header := map[string]string{"Accept": "application/vnd.github+json"}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		header["Authorization"] = "Bearer " + tok
	}
	var out []Candidate
	for page := 1; page <= githubPages; page++ {
		var rels []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
			Published  string `json:"published_at"`
		}
		u := fmt.Sprintf("%s%s/releases?per_page=100&page=%d", githubAPI, repo, page)
		if err := getJSON(ctx, u, header, &rels); err != nil {
			return nil, fmt.Errorf("github releases %s: %w", repo, err)
		}
		for _, r := range rels {
			if r.Draft {
				continue
			}
			c := Candidate{Name: r.Tag, Prerelease: r.Prerelease}
			if t, err := time.Parse(time.RFC3339, r.Published); err == nil {
				c.Published = t
			}
			out = append(out, c)
		}
		if len(rels) < 100 {
			break
		}
	}
	return out, nil
}

// splitSuite reads an apk/apt suite: "<release>/<repository>" (v3.22/main, trixie/main).
func splitSuite(suite string) (release, repo string, err error) {
	release, repo, ok := strings.Cut(suite, "/")
	if !ok || release == "" || repo == "" {
		return "", "", fmt.Errorf("suite %q: want <release>/<repository>, e.g. v3.22/main or trixie/main", suite)
	}
	return release, repo, nil
}

func listApk(ctx context.Context, pkg, suite string) ([]Candidate, error) {
	release, repo, err := splitSuite(suite)
	if err != nil {
		return nil, err
	}
	resp, err := get(ctx, fmt.Sprintf("%s%s/%s/x86_64/APKINDEX.tar.gz", alpineBase, release, repo), nil)
	if err != nil {
		return nil, fmt.Errorf("apk %s: %w", pkg, err)
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxIndex))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("apk %s: APKINDEX missing from the index archive", pkg)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == "APKINDEX" {
			return stanzas(tr, "P:", "V:", "t:", pkg)
		}
	}
}

func listApt(ctx context.Context, pkg, suite string) ([]Candidate, error) {
	release, repo, err := splitSuite(suite)
	if err != nil {
		return nil, err
	}
	resp, err := get(ctx, fmt.Sprintf("%s%s/%s/binary-amd64/Packages.gz", debianBase, release, repo), nil)
	if err != nil {
		return nil, fmt.Errorf("apt %s: %w", pkg, err)
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxIndex))
	if err != nil {
		return nil, err
	}
	return stanzas(gz, "Package: ", "Version: ", "", pkg)
}

// stanzas reads a blank-line separated package index, returning the versions of pkg.
// timeKey, when set, names a unix build-time line.
func stanzas(r io.Reader, nameKey, versionKey, timeKey, pkg string) ([]Candidate, error) {
	var out []Candidate
	var n, v string
	var t time.Time
	flush := func() {
		if n == pkg && v != "" {
			out = append(out, Candidate{Name: v, Published: t})
		}
		n, v, t = "", "", time.Time{}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, nameKey):
			n = strings.TrimPrefix(line, nameKey)
		case strings.HasPrefix(line, versionKey):
			v = strings.TrimPrefix(line, versionKey)
		case timeKey != "" && strings.HasPrefix(line, timeKey):
			if secs, err := strconv.ParseInt(strings.TrimPrefix(line, timeKey), 10, 64); err == nil {
				t = time.Unix(secs, 0).UTC()
			}
		}
	}
	flush()
	return out, sc.Err()
}

// listURL reads a release page (the uscan model): every distinct match of select.tags on the
// page is a candidate.
func listURL(ctx context.Context, src Source) ([]Candidate, error) {
	resp, err := get(ctx, src.Location, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxIndex))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Candidate
	for _, m := range src.Select.Tags.FindAllString(string(page), -1) {
		if !seen[m] {
			seen[m] = true
			out = append(out, Candidate{Name: m})
		}
	}
	return out, nil
}
