package registry

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Quay implements the Registry interface for Quay.io (and self-hosted Quay/Red Hat Quay).
// Uses the Quay REST API (/api/v1/repository).
// Requires an OAuth token or robot account token with repo:admin scope.
type Quay struct {
	client  httpClient
	baseURL string
}

func NewQuay(registryURL, user, pass string) *Quay {
	base := "https://quay.io"
	if registryURL != "" && registryURL != "quay.io" {
		base = normalizeURL(registryURL)
	}

	return &Quay{
		client: httpClient{
			base: base,
			headers: map[string]string{
				"Authorization": "Bearer " + pass,
			},
		},
		baseURL: base,
	}
}

func (q *Quay) Provider() string { return "quay" }

func (q *Quay) ListTags(ctx context.Context, repo string) ([]TagInfo, error) {
	var allTags []TagInfo
	page := 1
	hasMore := true

	for hasMore {
		var resp struct {
			Tags []struct {
				Name           string `json:"name"`
				Digest         string `json:"manifest_digest"`
				LastModified   string `json:"last_modified"`
				StartTimestamp int64  `json:"start_ts"`
			} `json:"tags"`
			HasAdditional bool `json:"has_additional"`
		}

		apiURL := fmt.Sprintf("%s/api/v1/repository/%s/tag/?limit=100&page=%d&onlyActiveTags=true",
			q.baseURL, url.PathEscape(repo), page)

		_, err := q.client.doJSON(ctx, "GET", apiURL, nil, &resp)
		if err != nil {
			return nil, fmt.Errorf("quay: listing tags for %s: %w", repo, err)
		}

		for _, t := range resp.Tags {
			created := time.Unix(t.StartTimestamp, 0)
			if t.LastModified != "" {
				if parsed, err := time.Parse("Mon, 02 Jan 2006 15:04:05 -0700", t.LastModified); err == nil {
					created = parsed
				}
			}
			allTags = append(allTags, TagInfo{
				Name:      t.Name,
				Digest:    t.Digest,
				CreatedAt: created,
			})
		}

		hasMore = resp.HasAdditional
		page++
	}

	return allTags, nil
}

func (q *Quay) DeleteTag(ctx context.Context, repo string, tag string) error {
	apiURL := fmt.Sprintf("%s/api/v1/repository/%s/tag/%s",
		q.baseURL, url.PathEscape(repo), url.PathEscape(tag))

	_, err := q.client.doJSON(ctx, "DELETE", apiURL, nil, nil)
	if err != nil {
		return fmt.Errorf("quay: deleting tag %s in %s: %w", tag, repo, err)
	}
	return nil
}

func (q *Quay) UpdateDescription(ctx context.Context, repo, short, full string) error {
	// Quay's description is a single MARKDOWN field (the repo overview / long body), not a
	// short tagline — prefer the full markdown, fall back to short (same shape as Harbor).
	desc := full
	if desc == "" {
		desc = short
	}
	payload := map[string]string{
		"description": desc,
	}

	apiURL := fmt.Sprintf("%s/api/v1/repository/%s", q.baseURL, url.PathEscape(repo))
	_, err := q.client.doJSON(ctx, "PUT", apiURL, payload, nil)
	if err != nil {
		return fmt.Errorf("quay: updating description for %s: %w", repo, err)
	}
	return nil
}

// normalizeURL ensures a URL has a scheme.
func normalizeURL(u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return strings.TrimRight(u, "/")
	}
	return "https://" + strings.TrimRight(u, "/")
}
