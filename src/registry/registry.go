// Package registry provides a platform-agnostic abstraction over container
// registries (Docker Hub, GitLab, GHCR, Quay, JFrog, Harbor, Gitea).
// Every registry operation (list tags, delete tags) goes through the Registry
// interface so StageFreight's retention engine works identically regardless
// of where images are hosted.
package registry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// Registry is the interface every container registry provider implements.
type Registry interface {
	// Provider returns the registry vendor name.
	Provider() string

	// ListTags returns all tags for a repository, sorted by creation time descending.
	ListTags(ctx context.Context, repo string) ([]TagInfo, error)

	// DeleteTag removes a single tag from a repository.
	DeleteTag(ctx context.Context, repo string, tag string) error

	// UpdateDescription pushes short and full descriptions to the registry.
	// Returns nil for providers that don't support description APIs.
	UpdateDescription(ctx context.Context, repo, short, full string) error
}

// Warner is an optional interface for registries that emit warnings
// (e.g., credential hygiene nudges). Callers can type-assert to check.
type Warner interface {
	Warnings() []string
}

// TagInfo describes a single tag in a container registry.
type TagInfo struct {
	Name      string
	Digest    string
	CreatedAt time.Time
}

// NormalizeProvider maps provider aliases to their canonical platform names.
// Canonical names are the platform brand: docker, github, gitlab, quay, jfrog, harbor, gitea.
// Legacy aliases (dockerhub, ghcr) are accepted and mapped to canonical forms.
func NormalizeProvider(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "dockerhub":
		return "docker"
	case "ghcr":
		return "github"
	default:
		return p
	}
}

// NewRegistry creates a registry client for the given provider.
// Credentials are resolved from environment variables using the prefix:
//
//	prefix: "DOCKER" → DOCKER_USER / DOCKER_PASS
//	prefix: "GHCR_ORG" → GHCR_ORG_USER / GHCR_ORG_PASS
//
// The registryURL is the base URL (e.g., "docker.io", "ghcr.io").
func NewRegistry(provider, registryURL, credentialPrefix string) (Registry, error) {
	provider = NormalizeProvider(provider)
	user, pass := resolveCredentials(credentialPrefix)

	switch provider {
	case "local":
		return NewLocal(), nil
	case "docker":
		return NewDockerHub(user, pass), nil
	case "gitlab":
		return NewGitLab(registryURL, user, pass), nil
	case "github":
		return NewGHCR(user, pass), nil
	case "quay":
		return NewQuay(registryURL, user, pass), nil
	case "jfrog":
		return NewJFrog(registryURL, user, pass), nil
	case "harbor":
		return NewHarbor(registryURL, user, pass), nil
	case "gitea", "forgejo":
		return NewGitea(registryURL, user, pass), nil
	default:
		return nil, fmt.Errorf("registry: unsupported provider %q (valid: docker, github, gitlab, quay, jfrog, harbor, gitea, forgejo)", provider)
	}
}

// resolveCredentials reads USERNAME and PASSWORD from env vars using the
// configured prefix. Returns empty strings if no prefix or vars are unset.
func resolveCredentials(prefix string) (user, pass string) {
	if prefix == "" {
		return "", ""
	}
	p := strings.ToUpper(prefix)
	pass = os.Getenv(p + "_PASS")
	if pass == "" {
		pass = os.Getenv(p + "_TOKEN")
	}
	return os.Getenv(p + "_USER"), pass
}
