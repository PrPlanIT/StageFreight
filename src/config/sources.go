package config

import (
	"fmt"
	"net/url"
	"strings"
)

// SyncConfig declares which sync domains a mirror repo receives.
// Git mirror is the foundation; artifact projection is subordinate and
// gated on mirror success for the same accessory.
type SyncConfig struct {
	// Git enables authoritative mirror replication via git push --mirror.
	// All refs, branches, tags, deletions, force updates. This is the
	// foundation — artifact sync runs only after mirror succeeds.
	Git bool `yaml:"git,omitempty"`

	// Releases enables forge-native release projection (notes, assets, links).
	// Runs after git mirror succeeds. Tag is the identity key.
	Releases bool `yaml:"releases,omitempty"`

	// Docs enables README/doc file projection via forge commit API.
	// Mutually exclusive with Git (docs arrive through git mirror).
	// Only valid when Git is false.
	Docs bool `yaml:"docs,omitempty"`
}

// resolvePublishOriginRepo finds the repo that serves as publish origin.
// Lookup order: repo with publish-origin role → primary (fallback).
// Validation guarantees at most one publish-origin exists.
func resolvePublishOriginRepo(cfg *Config) (*ResolvedRepo, error) {
	repo := FindRepoWithRole(cfg.Repos, "publish-origin")
	if repo == nil {
		repo = FindRepoWithRole(cfg.Repos, "primary")
	}
	if repo == nil {
		return nil, fmt.Errorf("publish-origin: no repo with publish-origin or primary role")
	}
	resolved, err := ResolveRepo(*repo, cfg.Forges, cfg.Vars)
	if err != nil {
		return nil, fmt.Errorf("publish-origin: %w", err)
	}
	return resolved, nil
}

// ResolvePublishOrigin resolves the serving base URL for rendered artifacts.
// Uses the repo with publish-origin role, falling back to primary.
func ResolvePublishOrigin(cfg *Config) (string, error) {
	branch := PrimaryDefaultBranch(cfg)
	if branch == "" {
		return "", fmt.Errorf("publish-origin: primary repo default_branch is required")
	}
	resolved, err := resolvePublishOriginRepo(cfg)
	if err != nil {
		return "", err
	}
	return ForgeRawBase(resolved.Provider, resolved.BaseURL, resolved.Project, branch)
}

// ResolveLinkBase resolves the page-link (blob) base URL for relative links.
// Uses the repo with publish-origin role, falling back to primary.
func ResolveLinkBase(cfg *Config) (string, error) {
	branch := PrimaryDefaultBranch(cfg)
	if branch == "" {
		return "", fmt.Errorf("publish-origin: primary repo default_branch is required")
	}
	resolved, err := resolvePublishOriginRepo(cfg)
	if err != nil {
		return "", err
	}
	return ForgeLinkBase(resolved.Provider, resolved.BaseURL, resolved.Project, branch)
}


// ForgeRawBase constructs a raw content base URL from forge mirror fields.
// Handles GitLab subgroup paths (group/subgroup/project) correctly.
// All inputs are normalized to prevent double-slash artifacts.
func ForgeRawBase(provider, baseURL, projectID, branch string) (string, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	projectID = strings.Trim(projectID, "/")
	branch = strings.Trim(branch, "/")
	switch provider {
	case "github":
		host := strings.Replace(baseURL, "github.com", "raw.githubusercontent.com", 1)
		return fmt.Sprintf("%s/%s/%s", host, projectID, branch), nil
	case "gitlab":
		// Works for subgroups: gitlab.com/group/subgroup/repo/-/raw/main
		return fmt.Sprintf("%s/%s/-/raw/%s", baseURL, projectID, branch), nil
	case "gitea":
		return fmt.Sprintf("%s/%s/raw/branch/%s", baseURL, projectID, branch), nil
	default:
		return "", fmt.Errorf("unsupported forge provider %q for raw URL derivation", provider)
	}
}

// ForgeLinkBase constructs a page-link (blob) base URL from forge fields.
// Used for resolving relative links (e.g., LICENSE → blob URL).
func ForgeLinkBase(provider, baseURL, projectID, branch string) (string, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	projectID = strings.Trim(projectID, "/")
	branch = strings.Trim(branch, "/")
	switch provider {
	case "github":
		return fmt.Sprintf("%s/%s/blob/%s", baseURL, projectID, branch), nil
	case "gitlab":
		return fmt.Sprintf("%s/%s/-/blob/%s", baseURL, projectID, branch), nil
	case "gitea":
		return fmt.Sprintf("%s/%s/src/branch/%s", baseURL, projectID, branch), nil
	default:
		return "", fmt.Errorf("unsupported forge provider %q for link base derivation", provider)
	}
}

// ParseForgeURL detects forge provider from a full repository URL,
// extracts base URL and project ID.
// Examples:
//
//	"https://github.com/PrPlanIT/StageFreight"       → github, "https://github.com", "PrPlanIT/StageFreight"
//	"https://gitlab.prplanit.com/SoFMeRight/dungeon" → gitlab, "https://gitlab.prplanit.com", "SoFMeRight/dungeon"
func ParseForgeURL(rawURL string) (provider, baseURL, projectID string, err error) {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return "", "", "", fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	path := strings.TrimPrefix(u.Path, "/")
	if path == "" {
		return "", "", "", fmt.Errorf("URL %q has no project path", rawURL)
	}
	base := u.Scheme + "://" + u.Host
	if strings.Contains(u.Host, "github.com") {
		return "github", base, path, nil
	}
	if strings.Contains(u.Host, "gitlab") {
		return "gitlab", base, path, nil
	}
	if strings.Contains(u.Host, "gitea") || strings.Contains(u.Host, "codeberg") {
		return "gitea", base, path, nil
	}
	return "", "", "", fmt.Errorf(
		"cannot detect forge provider from URL %q — use kind: mirror (with explicit provider) or kind: url instead",
		rawURL)
}
