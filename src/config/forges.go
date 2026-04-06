package config

import "fmt"

// ForgeConfig declares a git host. Declared once, referenced by repos.
// A forge is an identity — provider type, base URL, credentials.
type ForgeConfig struct {
	ID          string `yaml:"id"`                    // unique identifier (e.g., "prplanit-gitlab")
	Provider    string `yaml:"provider"`              // gitlab, github, gitea
	URL         string `yaml:"url"`                   // base URL (e.g., "https://gitlab.prplanit.com")
	Credentials string `yaml:"credentials,omitempty"` // env var prefix for token resolution
}

// RepoConfig declares a project on a forge. References forge by id.
// Role is explicit: primary, mirror, or omitted (available but no role).
type RepoConfig struct {
	ID            string     `yaml:"id"`                       // unique identifier
	Forge         string     `yaml:"forge"`                    // references forges[].id
	Project       string     `yaml:"project"`                  // project path on the forge (e.g., "{var:gitlab_group}/{var:repo}")
	Role          string     `yaml:"role,omitempty"`           // "primary" | "mirror" | "" (available)
	DefaultBranch string     `yaml:"default_branch,omitempty"` // e.g., "main"
	Worktree      string     `yaml:"worktree,omitempty"`       // local working tree path (primary only)
	Ref           string     `yaml:"ref,omitempty"`            // pinned ref for non-primary repos (governance, presets)
	Sync          SyncConfig `yaml:"sync,omitempty"`           // mirror sync domains
}

// RegistryConfig declares an OCI registry host. Declared once, referenced by targets.
type RegistryConfig struct {
	ID          string `yaml:"id"`                    // unique identifier (e.g., "dockerhub")
	Provider    string `yaml:"provider"`              // docker, harbor, ghcr, quay, gitea, generic
	URL         string `yaml:"url"`                   // registry URL (e.g., "docker.io")
	Credentials string `yaml:"credentials,omitempty"` // env var prefix for token resolution
	DefaultPath string `yaml:"default_path,omitempty"` // default image path (e.g., "{var:org}/{var:repo}")
}

// PublishOriginConfig declares where rendered artifacts are served from.
// Discriminated by Kind:
//   - "repo": derive URLs from repos[ref] + forge identity
//   - "url": explicit base URL, no repo reference
type PublishOriginConfig struct {
	Kind string `yaml:"kind"`           // "repo" or "url"
	Ref  string `yaml:"ref,omitempty"`  // references repos[].id (kind: repo)
	Base string `yaml:"base,omitempty"` // explicit base URL (kind: url)
}

// ValidateIdentityGraph checks structural invariants of forges, repos, and registries.
// Returns all errors found (not just the first).
func ValidateIdentityGraph(forges []ForgeConfig, repos []RepoConfig, registries []RegistryConfig) []string {
	var errs []string

	// Forge IDs unique.
	forgeIDs := make(map[string]bool)
	for _, f := range forges {
		if f.ID == "" {
			errs = append(errs, "forges: entry missing id")
			continue
		}
		if forgeIDs[f.ID] {
			errs = append(errs, fmt.Sprintf("forges: duplicate id %q", f.ID))
		}
		forgeIDs[f.ID] = true

		if f.Provider == "" {
			errs = append(errs, fmt.Sprintf("forges[%s]: provider is required", f.ID))
		}
		if f.URL == "" {
			errs = append(errs, fmt.Sprintf("forges[%s]: url is required", f.ID))
		}
	}

	// Repo IDs unique + forge references valid + exactly one primary.
	repoIDs := make(map[string]bool)
	primaryCount := 0
	for _, r := range repos {
		if r.ID == "" {
			errs = append(errs, "repos: entry missing id")
			continue
		}
		if repoIDs[r.ID] {
			errs = append(errs, fmt.Sprintf("repos: duplicate id %q", r.ID))
		}
		repoIDs[r.ID] = true

		if r.Forge == "" {
			errs = append(errs, fmt.Sprintf("repos[%s]: forge is required", r.ID))
		} else if !forgeIDs[r.Forge] {
			errs = append(errs, fmt.Sprintf("repos[%s]: forge %q not found in forges", r.ID, r.Forge))
		}

		if r.Project == "" {
			errs = append(errs, fmt.Sprintf("repos[%s]: project is required", r.ID))
		}

		switch r.Role {
		case "primary":
			primaryCount++
			if r.DefaultBranch == "" {
				errs = append(errs, fmt.Sprintf("repos[%s]: default_branch is required for primary", r.ID))
			}
		case "mirror", "":
			// valid
		default:
			errs = append(errs, fmt.Sprintf("repos[%s]: unknown role %q (expected primary, mirror, or empty)", r.ID, r.Role))
		}

		if r.Worktree != "" && r.Role != "primary" {
			errs = append(errs, fmt.Sprintf("repos[%s]: worktree is only valid for primary repos", r.ID))
		}
	}

	if len(repos) > 0 && primaryCount == 0 {
		errs = append(errs, "repos: exactly one repo must have role: primary")
	}
	if primaryCount > 1 {
		errs = append(errs, fmt.Sprintf("repos: exactly one primary allowed, found %d", primaryCount))
	}

	// Mirror constraints.
	for _, r := range repos {
		if r.Role == "mirror" {
			if r.Worktree != "" {
				errs = append(errs, fmt.Sprintf("repos[%s]: mirrors cannot have worktree", r.ID))
			}
		}
	}

	// Registry IDs unique + required fields.
	registryIDs := make(map[string]bool)
	for _, r := range registries {
		if r.ID == "" {
			errs = append(errs, "registries: entry missing id")
			continue
		}
		if registryIDs[r.ID] {
			errs = append(errs, fmt.Sprintf("registries: duplicate id %q", r.ID))
		}
		registryIDs[r.ID] = true

		if r.URL == "" {
			errs = append(errs, fmt.Sprintf("registries[%s]: url is required", r.ID))
		}
		if r.Provider == "" {
			errs = append(errs, fmt.Sprintf("registries[%s]: provider is required", r.ID))
		}
	}

	// No mixed models — if new identity graph is used, legacy sources must not be.
	// (Enforced at call site in validate.go, not here.)

	return errs
}

// ValidateTargetRegistryRefs checks that targets with registry: reference existing registries,
// and that registry + inline fields don't conflict.
func ValidateTargetRegistryRefs(targets []TargetConfig, registries []RegistryConfig) []string {
	var errs []string
	registryIDs := make(map[string]bool)
	for _, r := range registries {
		registryIDs[r.ID] = true
	}

	for _, t := range targets {
		if t.Registry != "" {
			if !registryIDs[t.Registry] {
				errs = append(errs, fmt.Sprintf("targets[%s]: registry %q not found in registries", t.ID, t.Registry))
			}
			// Registry reference and inline fields must not coexist (except path override).
			if t.URL != "" {
				errs = append(errs, fmt.Sprintf("targets[%s]: url must not be set when registry is referenced", t.ID))
			}
			if t.Provider != "" {
				errs = append(errs, fmt.Sprintf("targets[%s]: provider must not be set when registry is referenced", t.ID))
			}
			if t.Credentials != "" {
				errs = append(errs, fmt.Sprintf("targets[%s]: credentials must not be set when registry is referenced", t.ID))
			}
			// path is allowed — overrides default_path
		}

		// Registry targets must have identity from somewhere.
		if (t.Kind == "registry" || t.Kind == "docker-readme") && t.Registry == "" && t.URL == "" {
			errs = append(errs, fmt.Sprintf("targets[%s]: kind %s requires registry: or url:", t.ID, t.Kind))
		}

		// Inline-mode registry targets (no registry: ref) must be a complete identity.
		if (t.Kind == "registry" || t.Kind == "docker-readme") && t.Registry == "" && t.URL != "" {
			if t.Kind == "registry" && t.Path == "" {
				errs = append(errs, fmt.Sprintf("targets[%s]: inline registry target requires path:", t.ID))
			}
		}
	}

	return errs
}

// FindForgeByID returns the forge with the given id, or nil.
func FindForgeByID(forges []ForgeConfig, id string) *ForgeConfig {
	for i := range forges {
		if forges[i].ID == id {
			return &forges[i]
		}
	}
	return nil
}

// FindRepoByID returns the repo with the given id, or nil.
func FindRepoByID(repos []RepoConfig, id string) *RepoConfig {
	for i := range repos {
		if repos[i].ID == id {
			return &repos[i]
		}
	}
	return nil
}

// FindRepoByRole returns the first repo with the given role, or nil.
func FindRepoByRole(repos []RepoConfig, role string) *RepoConfig {
	for i := range repos {
		if repos[i].Role == role {
			return &repos[i]
		}
	}
	return nil
}

// FindRegistryByID returns the registry with the given id, or nil.
func FindRegistryByID(registries []RegistryConfig, id string) *RegistryConfig {
	for i := range registries {
		if registries[i].ID == id {
			return &registries[i]
		}
	}
	return nil
}

// MirrorRepos returns all repos with role "mirror".
func MirrorRepos(repos []RepoConfig) []RepoConfig {
	var mirrors []RepoConfig
	for _, r := range repos {
		if r.Role == "mirror" {
			mirrors = append(mirrors, r)
		}
	}
	return mirrors
}
