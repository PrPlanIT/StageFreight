package freshness

import (
	"context"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// dockerHubTagsResponse matches the Docker Hub v2 tags list API.
type dockerHubTagsResponse struct {
	Results []struct {
		Name string `json:"name"`
	} `json:"results"`
	Next *string `json:"next"`
}

// maxTagPages limits how many pages we fetch from Docker Hub to avoid
// excessive API calls for images with thousands of tags.
const maxTagPages = 10

// checkImages resolves base image freshness for all FROM stages.
func (m *freshnessModule) checkImages(ctx context.Context, file lint.FileInfo, stages []stageInfo) []Dependency {
	var deps []Dependency

	for _, stage := range stages {
		// Skip scratch and build-stage references (no colon, no registry).
		if stage.Image == "scratch" {
			continue
		}
		// Skip inter-stage references (e.g. "FROM builder")
		if !strings.Contains(stage.Image, ":") && !strings.Contains(stage.Image, "/") {
			continue
		}

		dep := m.resolveImage(ctx, file.Path, stage)
		if dep != nil {
			deps = append(deps, *dep)
		}
	}

	return deps
}

// resolveImage queries Docker Hub for available tags and computes
// the version delta for a single base image.
func (m *freshnessModule) resolveImage(ctx context.Context, filePath string, stage stageInfo) *Dependency {
	image, tag := splitImageTag(stage.Image)
	if tag == "" {
		tag = "latest"
	}

	namespace, repo := splitImageNamespace(image)

	dep := &Dependency{
		Name:      stage.Image,
		Current:   tag,
		Ecosystem: EcosystemDockerImage,
		File:      filePath,
		Line:      stage.Line,
	}

	// Fetch tags — use custom registry if configured, else Docker Hub public API.
	ep := m.cfg.registryEndpoint(EcosystemDockerImage)
	defaultURL := fmt.Sprintf("https://registry.hub.docker.com/v2/repositories/%s/%s/tags?page_size=100", namespace, repo)
	url := m.cfg.registryURL(EcosystemDockerImage, defaultURL)
	if url != defaultURL {
		// Custom registry: use v2 tags/list endpoint.
		url = strings.TrimRight(url, "/") + fmt.Sprintf("/%s/%s/tags/list", namespace, repo)
	}
	dep.SourceURL = url

	var tags []string
	pageURL := url
	for page := 0; page < maxTagPages && pageURL != ""; page++ {
		var resp dockerHubTagsResponse
		if err := m.http.fetchJSON(ctx, pageURL, &resp, ep); err != nil {
			if page == 0 {
				// First page failed — network errors are non-fatal.
				return dep
			}
			break
		}
		for _, r := range resp.Results {
			tags = append(tags, r.Name)
		}
		if resp.Next != nil {
			pageURL = *resp.Next
		} else {
			pageURL = ""
		}
	}

	// Decompose current tag and find latest in same suffix family.
	current := decomposeTag(tag)
	if current.Version == nil {
		// Non-versioned tag (e.g. "latest", "noble", "sha-...") — can't do
		// semver comparison. Digest tracking handled by lockfile.go.
		// Check for stable upgrade advisory.
		dep.Advisory = suggestStableUpgrade(current, tags)
		return dep
	}

	family := filterTagsByFamily(tags, current.Family)
	latest := latestInFamily(family)
	if latest == nil {
		return dep
	}

	dep.Latest = latest.Raw

	// Check for stable upgrade advisory on pre-release tags.
	if current.PreRank > 0 {
		dep.Advisory = suggestStableUpgrade(current, tags)
	}
	return dep
}

// suggestStableUpgrade checks if a non-versioned or pre-release tag has a
// matching stable release available by sampling the already-fetched registry
// tags. Returns an advisory message or empty string.
func suggestStableUpgrade(current decomposedTag, tags []string) string {
	// Group all tags by family, find families that have stable releases.
	type familyInfo struct {
		latest *decomposedTag
	}
	families := make(map[string]*familyInfo)

	for _, t := range tags {
		dt := decomposeTag(t)
		if dt.Version == nil || isDateLikeVersion(dt.Version) {
			continue
		}
		if dt.PreRank > 0 {
			continue // only count stable releases
		}
		fi, ok := families[dt.Family]
		if !ok {
			fi = &familyInfo{}
			families[dt.Family] = fi
		}
		if fi.latest == nil || dt.Version.GreaterThan(fi.latest.Version) {
			dtCopy := dt
			fi.latest = &dtCopy
		}
	}

	if len(families) == 0 {
		return ""
	}

	// For sha- tags: report that stable releases exist.
	if current.Family == "sha" {
		// Find the best stable family to suggest.
		var bestTag *decomposedTag
		for _, fi := range families {
			if fi.latest != nil {
				if bestTag == nil || fi.latest.Version.GreaterThan(bestTag.Version) {
					bestTag = fi.latest
				}
			}
		}
		if bestTag != nil {
			return fmt.Sprintf("stable releases available (e.g. %s)", bestTag.Raw)
		}
		return "stable releases available"
	}

	// For pre-release tags: check if a stable release exists in the same family.
	if current.PreRank > 0 {
		fi, ok := families[current.Family]
		if ok && fi.latest != nil {
			return fmt.Sprintf("stable release %s available (currently on pre-release)", fi.latest.Raw)
		}
		// Check bare family (empty string) as fallback.
		fi, ok = families[""]
		if ok && fi.latest != nil {
			return fmt.Sprintf("stable release %s available (currently on pre-release)", fi.latest.Raw)
		}
		return "stable release may be available — currently on pre-release channel"
	}

	// For other non-versioned tags (e.g. "latest", "noble"): check any stable.
	var bestTag *decomposedTag
	for _, fi := range families {
		if fi.latest != nil {
			if bestTag == nil || fi.latest.Version.GreaterThan(bestTag.Version) {
				bestTag = fi.latest
			}
		}
	}
	if bestTag != nil {
		return fmt.Sprintf("consider pinning to a versioned tag (e.g. %s)", bestTag.Raw)
	}
	return ""
}

// splitImageTag splits "golang:1.25-alpine" into ("golang", "1.25-alpine").
func splitImageTag(ref string) (string, string) {
	// Handle digest references (image@sha256:...)
	if idx := strings.Index(ref, "@"); idx >= 0 {
		return ref[:idx], ""
	}
	// Find the last colon that isn't part of a port in the registry URL.
	// "registry:5000/image:tag" → split at last colon.
	lastColon := strings.LastIndex(ref, ":")
	if lastColon < 0 {
		return ref, ""
	}
	// If colon is before a slash, it's a port not a tag separator.
	afterColon := ref[lastColon+1:]
	if strings.Contains(afterColon, "/") {
		return ref, ""
	}
	return ref[:lastColon], afterColon
}

// splitImageNamespace splits an image name into (namespace, repo) for Docker Hub.
// "golang" → ("library", "golang")
// "prplanit/stagefreight" → ("prplanit", "stagefreight")
// "ghcr.io/owner/repo" → not Docker Hub, skip.
func splitImageNamespace(image string) (string, string) {
	// Strip registry prefix if present.
	// Docker Hub images may have docker.io/ prefix.
	image = strings.TrimPrefix(image, "docker.io/")
	image = strings.TrimPrefix(image, "index.docker.io/")

	parts := strings.SplitN(image, "/", 2)
	if len(parts) == 1 {
		return "library", parts[0]
	}
	// If the first part looks like a registry (contains a dot), this isn't Docker Hub.
	if strings.Contains(parts[0], ".") {
		return parts[0], parts[1]
	}
	return parts[0], parts[1]
}
