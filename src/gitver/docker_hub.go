package gitver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TagInfo holds metadata for a single Docker Hub tag.
type TagInfo struct {
	Size        int64
	Digest      string
	LastUpdated time.Time
}

// DockerHubInfo holds metadata fetched from the Docker Hub API.
type DockerHubInfo struct {
	Pulls  int64              // total pull count
	Stars  int                // star count
	Size   int64              // compressed size of latest tag in bytes
	Latest string             // digest of latest tag (sha256:...)
	Tags   map[string]TagInfo // per-tag metadata
}

// FetchDockerHubInfo retrieves repository metadata from Docker Hub.
// namespace/repo format: "prplanit/stagefreight".
func FetchDockerHubInfo(namespace, repo string) (*DockerHubInfo, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	info := &DockerHubInfo{}

	// Fetch repository info (pulls, stars).
	repoURL := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/%s/", namespace, repo)
	resp, err := client.Get(repoURL)
	if err != nil {
		return nil, fmt.Errorf("docker hub repo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker hub repo: %s", resp.Status)
	}

	var repoData struct {
		PullCount int64 `json:"pull_count"`
		StarCount int   `json:"star_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repoData); err != nil {
		return nil, fmt.Errorf("docker hub repo decode: %w", err)
	}
	info.Pulls = repoData.PullCount
	info.Stars = repoData.StarCount

	// Fetch latest tag info (size, digest).
	tagURL := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/%s/tags/latest", namespace, repo)
	tagResp, err := client.Get(tagURL)
	if err == nil {
		defer tagResp.Body.Close()
		if tagResp.StatusCode == http.StatusOK {
			var tagData struct {
				FullSize int64  `json:"full_size"`
				Digest   string `json:"digest"`
				Images   []struct {
					Size   int64  `json:"size"`
					Digest string `json:"digest"`
				} `json:"images"`
			}
			if err := json.NewDecoder(tagResp.Body).Decode(&tagData); err == nil {
				info.Size = tagData.FullSize
				info.Latest = tagData.Digest
				// If no top-level size, sum from images.
				if info.Size == 0 && len(tagData.Images) > 0 {
					for _, img := range tagData.Images {
						info.Size += img.Size
					}
				}
				if info.Latest == "" && len(tagData.Images) > 0 {
					info.Latest = tagData.Images[0].Digest
				}
			}
		}
	}

	return info, nil
}

// FetchTagInfo retrieves metadata for specific tags from Docker Hub.
// Best-effort: tags that 404 or error are silently skipped.
func FetchTagInfo(client *http.Client, namespace, repo string, tags []string) map[string]TagInfo {
	result := make(map[string]TagInfo, len(tags))
	for _, tag := range tags {
		tagURL := fmt.Sprintf(
			"https://hub.docker.com/v2/repositories/%s/%s/tags/%s",
			namespace, repo, url.PathEscape(tag),
		)
		resp, err := client.Get(tagURL)
		if err != nil {
			continue
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			var data struct {
				FullSize    int64  `json:"full_size"`
				Digest      string `json:"digest"`
				LastUpdated string `json:"last_updated"`
				Images      []struct {
					Size   int64  `json:"size"`
					Digest string `json:"digest"`
				} `json:"images"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				return
			}
			ti := TagInfo{
				Size:   data.FullSize,
				Digest: data.Digest,
			}
			if ti.Size == 0 && len(data.Images) > 0 {
				for _, img := range data.Images {
					ti.Size += img.Size
				}
			}
			if ti.Digest == "" && len(data.Images) > 0 {
				ti.Digest = data.Images[0].Digest
			}
			if data.LastUpdated != "" {
				if t, err := time.Parse(time.RFC3339Nano, data.LastUpdated); err == nil {
					ti.LastUpdated = t
				}
			}
			result[tag] = ti
		}()
	}
	return result
}

// FormatTagField renders a per-tag metadata field for a {registry.<id>.tag.<tag>.<field>}
// token, from the provider-agnostic TagInfo the resolver assembles (Docker Hub API or OCI
// manifest). Recognized fields: size (human), size:raw (bytes), updated (YYYY-MM-DD), digest
// (short). Returns ("", false) for an unknown field so the caller leaves the token untouched.
func FormatTagField(ti TagInfo, field string) (string, bool) {
	switch field {
	case "size":
		return formatBytes(ti.Size), true
	case "size:raw":
		return strconv.FormatInt(ti.Size, 10), true
	case "updated":
		if ti.LastUpdated.IsZero() {
			return "", true
		}
		return ti.LastUpdated.Format("2006-01-02"), true
	case "digest":
		return shortDigest(ti.Digest), true
	}
	return "", false
}

// FormatCount renders a repo-level count (pulls, …) for human display: 1247 → "1.2k".
func FormatCount(n int64) string { return formatCount(n) }

// formatCount formats a number for human display: 1247 → "1.2k", 1234567 → "1.2M".
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.FormatInt(n, 10)
	}
}

// formatBytes formats bytes for human display: 75890432 → "72.4 MB".
func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// shortDigest returns the first 12 hex characters of a sha256:... digest.
func shortDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
