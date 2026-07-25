package registry

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// JFrog implements the Registry interface for JFrog Artifactory / JFrog Container Registry.
// Uses the Artifactory REST API for Docker registry management.
// Requires an admin token or user with delete permissions on the target repository.
type JFrog struct {
	client  httpClient
	baseURL string
}

func NewJFrog(registryURL, user, pass string) *JFrog {
	base := normalizeURL(registryURL)

	headers := map[string]string{}
	if pass != "" && user != "" {
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	} else if pass != "" {
		headers["Authorization"] = "Bearer " + pass
	}

	return &JFrog{
		client: httpClient{
			base:    base,
			headers: headers,
		},
		baseURL: base,
	}
}

func (j *JFrog) Provider() string { return "jfrog" }

func (j *JFrog) ListTags(ctx context.Context, repo string) ([]TagInfo, error) {
	// repo format: "repoKey/imagePath" (e.g., "docker-local/myorg/myapp")
	repoKey, imagePath := splitJFrogRepo(repo)

	var allTags []TagInfo

	var resp struct {
		Tags []string `json:"tags"`
	}

	apiURL := fmt.Sprintf("%s/v2/%s/%s/tags/list", j.baseURL, repoKey, imagePath)
	_, err := j.client.doJSON(ctx, "GET", apiURL, nil, &resp)
	if err != nil {
		return nil, fmt.Errorf("jfrog: listing tags for %s: %w", repo, err)
	}

	// Get creation time for each tag via storage API
	for _, tag := range resp.Tags {
		info := TagInfo{Name: tag}

		propURL := fmt.Sprintf("%s/api/storage/%s/%s/%s", j.baseURL, repoKey, imagePath, url.PathEscape(tag))
		var propResp struct {
			Created     string `json:"created"`
			LastUpdated string `json:"lastUpdated"`
		}
		if _, err := j.client.doJSON(ctx, "GET", propURL, nil, &propResp); err == nil {
			if propResp.LastUpdated != "" {
				if t, err := time.Parse(time.RFC3339, propResp.LastUpdated); err == nil {
					info.CreatedAt = t
				}
			} else if propResp.Created != "" {
				if t, err := time.Parse(time.RFC3339, propResp.Created); err == nil {
					info.CreatedAt = t
				}
			}
		}

		allTags = append(allTags, info)
	}

	return allTags, nil
}

func (j *JFrog) DeleteTag(ctx context.Context, repo string, tag string) error {
	repoKey, imagePath := splitJFrogRepo(repo)

	apiURL := fmt.Sprintf("%s/api/docker/%s/v2/%s/manifests/%s",
		j.baseURL, url.PathEscape(repoKey), imagePath, url.PathEscape(tag))

	_, err := j.client.doJSON(ctx, "DELETE", apiURL, nil, nil)
	if err != nil {
		return fmt.Errorf("jfrog: deleting tag %s in %s: %w", tag, repo, err)
	}
	return nil
}

// UpdateDescription sets the Artifactory repository's config "description" (the short
// public description) via a partial config update (POST /api/repositories/:repoKey).
// Artifactory's repository README is a UI feature with no documented REST API, so the
// long body (full) is NOT pushed here — only the short description.
func (j *JFrog) UpdateDescription(ctx context.Context, repo, short, _ string) error {
	if short == "" {
		return nil // nothing to set
	}
	repoKey, _ := splitJFrogRepo(repo)
	payload := map[string]string{"description": short}
	apiURL := fmt.Sprintf("%s/api/repositories/%s", j.baseURL, url.PathEscape(repoKey))
	if _, err := j.client.doJSON(ctx, "POST", apiURL, payload, nil); err != nil {
		return fmt.Errorf("jfrog: updating description for %s: %w", repoKey, err)
	}
	return nil
}

// splitJFrogRepo splits "repoKey/image/path" into the repo key and image path.
// If no slash, assumes the whole string is the image path in a "docker-local" repo.
func splitJFrogRepo(repo string) (repoKey, imagePath string) {
	idx := strings.IndexByte(repo, '/')
	if idx < 0 {
		return "docker-local", repo
	}
	return repo[:idx], repo[idx+1:]
}
