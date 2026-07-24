package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ProducerCoords are the repository coordinates a named-producer badge derives
// from the publish-origin repo (or an explicit repos: id override), so props like
// goreportcard / github-* stop restating {var:...} coordinates that already live
// in repos:.
type ProducerCoords struct {
	Provider string // github, gitlab, gitea, forgejo
	Host     string // github.com, gitlab.prplanit.com, ...
	Project  string // owner/name
	Module   string // host/owner/name (Go module path)
}

// ResolveProducerCoords resolves the coordinates for a named producer. When
// repoOverride names a repos: entry, that repo's coordinates are used; otherwise
// the publish-origin repo (role publish-origin, falling back to primary). This is
// what lets a named producer be self-contained — no module:/repo: param restating
// coordinates already declared once in repos:.
func ResolveProducerCoords(cfg *Config, repoOverride string) (*ProducerCoords, error) {
	var resolved *ResolvedRepo
	var err error
	if repoOverride != "" {
		repo := FindRepoByID(cfg.Repos, repoOverride)
		if repo == nil {
			return nil, fmt.Errorf("producer repo override %q not found in repos:", repoOverride)
		}
		resolved, err = ResolveRepo(*repo, cfg.Forges, cfg.Vars)
	} else {
		resolved, err = resolvePublishOriginRepo(cfg)
	}
	if err != nil {
		return nil, err
	}

	host := forgeHost(resolved.BaseURL)
	return &ProducerCoords{
		Provider: resolved.Provider,
		Host:     host,
		Project:  resolved.Project,
		Module:   host + "/" + resolved.Project,
	}, nil
}

// forgeHost extracts the bare host (no scheme, no path) from a forge base URL.
func forgeHost(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return strings.TrimSuffix(strings.TrimPrefix(baseURL, "https://"), "/")
	}
	return u.Host
}
