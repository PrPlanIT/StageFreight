package registry

import (
	"fmt"
	"regexp"
	"strings"
)

// ResolvedRegistryTarget is a fully resolved registry target with enough
// information to both push images and generate deterministic UI URLs.
// If StageFreight can publish to this target, it can generate correct URLs.
type ResolvedRegistryTarget struct {
	Provider string   // canonical provider: docker, github, gitlab, quay, jfrog, harbor, gitea, generic
	Host     string   // normalized registry host (e.g., "docker.io", "registry.gitlab.com")
	Path     string   // resolved repo/image path (e.g., "prplanit/stagefreight")
	Tags     []string // fully resolved publish tags
}

// NormalizeHost strips scheme prefixes and trailing slashes from a registry host.
// Prevents config variations like "https://ghcr.io" from producing broken URLs.
func NormalizeHost(h string) string {
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimSuffix(h, "/")
	return h
}

// ImageRef returns the canonical image reference (host/path).
func (r ResolvedRegistryTarget) ImageRef() string {
	return fmt.Sprintf("%s/%s", r.Host, r.Path)
}

// DisplayName returns the human-friendly registry label.
func (r ResolvedRegistryTarget) DisplayName() string {
	switch r.Provider {
	case "docker":
		return "Docker Hub"
	case "github":
		return "GitHub Container Registry"
	case "quay":
		return "Quay.io"
	case "gitlab":
		return "GitLab Registry"
	case "jfrog":
		return "JFrog Artifactory"
	case "harbor":
		return "Harbor"
	case "gitea":
		return "Gitea Registry"
	case "forgejo":
		return "Forgejo Registry"
	case "ecr":
		return "Amazon ECR"
	case "gar":
		return "Google Artifact Registry"
	case "acr":
		return "Azure Container Registry"
	case "nexus":
		return "Sonatype Nexus"
	case "generic":
		return r.Host
	default:
		return r.Host
	}
}

// RepoURL returns the web UI URL for this image's repository page.
// Every supported provider returns a deterministic URL.
// Generic provider returns https://{host}/{path} (the user's configured URL).
// Panics on unknown providers — if StageFreight can publish, it must resolve URLs.
func (r ResolvedRegistryTarget) RepoURL() string {
	switch r.Provider {
	case "docker":
		return fmt.Sprintf("https://hub.docker.com/r/%s", r.Path)
	case "github":
		owner, pkg := splitPath(r.Path)
		return fmt.Sprintf("https://github.com/%s/packages/container/package/%s", owner, pkg)
	case "quay":
		return fmt.Sprintf("https://quay.io/repository/%s", r.Path)
	case "gitlab":
		base := deriveGitLabWebBase(r.Host)
		return fmt.Sprintf("%s/%s/container_registry", base, r.Path)
	case "jfrog":
		return fmt.Sprintf("https://%s/ui/repos/tree/General/%s", r.Host, r.Path)
	case "harbor":
		project, repo := splitPath(r.Path)
		return fmt.Sprintf("https://%s/harbor/projects/%s/repositories/%s", r.Host, project, repo)
	case "gitea", "forgejo":
		owner, pkg := splitPath(r.Path)
		return fmt.Sprintf("https://%s/%s/-/packages/container/%s", r.Host, owner, pkg)
	case "ecr":
		account, region, ok := parseECRHost(r.Host)
		if !ok {
			return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
		}
		return fmt.Sprintf("https://%s.console.aws.amazon.com/ecr/repositories/private/%s/%s", region, account, r.Path)
	case "gar":
		region, ok := parseGARHost(r.Host)
		if !ok {
			return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
		}
		parts := strings.SplitN(r.Path, "/", 3)
		if len(parts) < 3 {
			return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
		}
		return fmt.Sprintf("https://console.cloud.google.com/artifacts/docker/%s/%s/%s/%s", parts[0], region, parts[1], parts[2])
	case "acr":
		return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
	case "nexus":
		return fmt.Sprintf("https://%s/#browse/search/docker==%s", r.Host, r.Path)
	case "generic":
		return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
	default:
		// Presentation layer: an unrecognized provider must NEVER crash a
		// completed, verified publication (a release-notes URL is not on the
		// integrity path). Degrade to a generic repo URL. The "every configured
		// provider is supported" invariant is enforced at config-validation time,
		// not by a runtime panic here.
		return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
	}
}

// TagURL returns the web UI URL for a specific tag on this image.
// Every supported provider returns a deterministic URL.
// Generic provider returns https://{host}/{path} (best available reference).
// Panics on unknown providers — if StageFreight can publish, it must resolve URLs.
func (r ResolvedRegistryTarget) TagURL(tag string) string {
	switch r.Provider {
	case "docker":
		return fmt.Sprintf("https://hub.docker.com/r/%s/tags?name=%s", r.Path, tag)
	case "github":
		// GHCR packages page doesn't have per-tag deep links
		owner, pkg := splitPath(r.Path)
		return fmt.Sprintf("https://github.com/%s/packages/container/package/%s", owner, pkg)
	case "quay":
		return fmt.Sprintf("https://quay.io/repository/%s?tab=tags&tag=%s", r.Path, tag)
	case "gitlab":
		base := deriveGitLabWebBase(r.Host)
		return fmt.Sprintf("%s/%s/container_registry", base, r.Path)
	case "jfrog":
		return fmt.Sprintf("https://%s/ui/repos/tree/General/%s", r.Host, r.Path)
	case "harbor":
		project, repo := splitPath(r.Path)
		return fmt.Sprintf("https://%s/harbor/projects/%s/repositories/%s/artifacts-tab", r.Host, project, repo)
	case "gitea", "forgejo":
		owner, pkg := splitPath(r.Path)
		return fmt.Sprintf("https://%s/%s/-/packages/container/%s", r.Host, owner, pkg)
	case "ecr":
		account, region, ok := parseECRHost(r.Host)
		if !ok {
			return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
		}
		return fmt.Sprintf("https://%s.console.aws.amazon.com/ecr/repositories/private/%s/%s/_/image/%s/details", region, account, r.Path, tag)
	case "gar":
		return r.RepoURL() // GAR has no per-tag deep link
	case "acr":
		return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
	case "nexus":
		return fmt.Sprintf("https://%s/#browse/search/docker==%s", r.Host, r.Path)
	case "generic":
		return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
	default:
		// Presentation layer: degrade, never panic — see RepoURL. A release-notes
		// link defect must not invalidate a completed, verified publication.
		return fmt.Sprintf("https://%s/%s", r.Host, r.Path)
	}
}

// deriveGitLabWebBase converts a GitLab registry host to its web UI base.
// e.g., "registry.gitlab.com" → "https://gitlab.com"
//
//	"registry.gitlab.example.com" → "https://gitlab.example.com"
func deriveGitLabWebBase(registryHost string) string {
	host := strings.ToLower(registryHost)
	host = strings.TrimPrefix(host, "registry.")
	return "https://" + host
}

// splitPath splits "owner/repo" into (owner, repo).
// For deeper paths like "owner/repo/sub", owner is the first component
// and repo is everything after.
func splitPath(path string) (string, string) {
	if idx := strings.IndexByte(path, '/'); idx >= 0 {
		return path[:idx], path[idx+1:]
	}
	return path, ""
}

// ecrHostRe matches ECR hosts: {account}.dkr.ecr.{region}.amazonaws.com
var ecrHostRe = regexp.MustCompile(`^(\d+)\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com$`)

// parseECRHost extracts account and region from an ECR host.
func parseECRHost(host string) (account, region string, ok bool) {
	m := ecrHostRe.FindStringSubmatch(host)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// garHostRe matches GAR hosts: {region}-docker.pkg.dev
var garHostRe = regexp.MustCompile(`^([a-z0-9-]+)-docker\.pkg\.dev$`)

// parseGARHost extracts the region from a GAR host.
func parseGARHost(host string) (region string, ok bool) {
	m := garHostRe.FindStringSubmatch(host)
	if m == nil {
		return "", false
	}
	return m[1], true
}
