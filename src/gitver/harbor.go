package gitver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HarborInfo holds metadata fetched from a Harbor v2 registry, used to resolve
// {harbor.*} badge tokens. Harbor is internal (no shields.io coverage), so these
// values are fetched with the HARBOR credential and rendered into committed SVGs —
// the same path {docker.*} takes for Docker Hub.
type HarborInfo struct {
	Pulls        int64  // repository pull count
	LatestStable string // highest semver tag, without the leading v (e.g. "1.2.3")
	LatestDev    string // the dev-<sha> tag on the latest-dev artifact (e.g. "dev-abc1234")
}

var harborStableTagRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// FetchHarborInfo retrieves repository metadata from a Harbor v2 registry.
// baseURL is the registry host (e.g. "cr.pcfae.com"); project/repo are the two
// path components (e.g. "prplanit" / "homelabhelpdesk.com"). user/secret are the
// HARBOR credential — when both are set, requests carry HTTP Basic auth.
func FetchHarborInfo(baseURL, project, repo, user, secret string) (*HarborInfo, error) {
	base := normalizeHarborURL(baseURL)
	client := &http.Client{Timeout: 10 * time.Second}

	auth := ""
	if user != "" && secret != "" {
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+secret))
	}
	get := func(apiURL string, out any) error {
		req, err := http.NewRequest(http.MethodGet, apiURL, nil)
		if err != nil {
			return err
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("harbor: %s: %s", apiURL, resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	info := &HarborInfo{}

	// Repository-level pull count (best-effort — a 404/403 here must not sink the
	// tag derivation below).
	repoURL := fmt.Sprintf("%s/api/v2.0/projects/%s/repositories/%s",
		base, url.PathEscape(project), url.PathEscape(repo))
	var repoData struct {
		PullCount int64 `json:"pull_count"`
	}
	if err := get(repoURL, &repoData); err == nil {
		info.Pulls = repoData.PullCount
	}

	// Artifacts → tags. Tags are grouped by artifact so the dev tag can be tied to
	// the same artifact latest-dev points at.
	var stableTags []string
	type devEntry struct {
		push         time.Time
		devTag       string
		hasLatestDev bool
	}
	var devs []devEntry

	page := 1
	for {
		var raw []struct {
			PushTime time.Time `json:"push_time"`
			Tags     []struct {
				Name string `json:"name"`
			} `json:"tags"`
		}
		apiURL := fmt.Sprintf("%s/api/v2.0/projects/%s/repositories/%s/artifacts?page=%d&page_size=100&with_tag=true",
			base, url.PathEscape(project), url.PathEscape(repo), page)
		if err := get(apiURL, &raw); err != nil {
			// Nothing usable at all → surface the error; otherwise return what we have.
			if page == 1 && info.Pulls == 0 {
				return nil, err
			}
			break
		}
		if len(raw) == 0 {
			break
		}
		for _, a := range raw {
			var d devEntry
			d.push = a.PushTime
			for _, t := range a.Tags {
				if harborStableTagRe.MatchString(t.Name) {
					stableTags = append(stableTags, t.Name)
				}
				if strings.HasPrefix(t.Name, "dev-") {
					d.devTag = t.Name
				}
				if t.Name == "latest-dev" {
					d.hasLatestDev = true
				}
			}
			if d.devTag != "" {
				devs = append(devs, d)
			}
		}
		page++
	}

	info.LatestStable = harborHighestStable(stableTags)

	// Prefer the dev tag on the latest-dev artifact; fall back to the newest
	// dev-tagged artifact by push time.
	for _, d := range devs {
		if d.hasLatestDev {
			info.LatestDev = d.devTag
			break
		}
	}
	if info.LatestDev == "" && len(devs) > 0 {
		sort.Slice(devs, func(i, j int) bool { return devs[i].push.After(devs[j].push) })
		info.LatestDev = devs[0].devTag
	}

	return info, nil
}

// harborHighestStable returns the highest X.Y.Z tag (leading v stripped), or "".
func harborHighestStable(tags []string) string {
	best := ""
	var bh [3]int
	for _, t := range tags {
		m := harborStableTagRe.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		var v [3]int
		v[0], _ = strconv.Atoi(m[1])
		v[1], _ = strconv.Atoi(m[2])
		v[2], _ = strconv.Atoi(m[3])
		if best == "" || v[0] > bh[0] ||
			(v[0] == bh[0] && (v[1] > bh[1] || (v[1] == bh[1] && v[2] > bh[2]))) {
			best = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
			bh = v
		}
	}
	return best
}

// normalizeHarborURL trims a trailing slash and defaults the scheme to https.
func normalizeHarborURL(raw string) string {
	raw = strings.TrimRight(raw, "/")
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return raw
}

// ResolveHarborTemplates replaces {harbor.*} tokens with values from Harbor.
// Returns s unchanged if info is nil or no {harbor.} token is present.
func ResolveHarborTemplates(s string, info *HarborInfo) string {
	if info == nil || !strings.Contains(s, "{harbor.") {
		return s
	}
	s = strings.ReplaceAll(s, "{harbor.pulls:raw}", strconv.FormatInt(info.Pulls, 10))
	s = strings.ReplaceAll(s, "{harbor.pulls}", formatCount(info.Pulls))
	s = strings.ReplaceAll(s, "{harbor.version}", info.LatestStable)
	s = strings.ReplaceAll(s, "{harbor.dev}", info.LatestDev)
	return s
}
