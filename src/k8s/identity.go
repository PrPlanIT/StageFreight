package k8s

import (
	"sort"
	"strings"
)

// ComponentRole classifies a workload's function within an application.
type ComponentRole string

const (
	RolePrimary   ComponentRole = "primary"
	RoleDatabase  ComponentRole = "database"
	RoleCache     ComponentRole = "cache"
	RoleQueue     ComponentRole = "queue"
	RoleSearch    ComponentRole = "search"
	RoleSentinel  ComponentRole = "sentinel"
	RoleProxy     ComponentRole = "proxy"
	RoleWorker    ComponentRole = "worker"
	RoleSecurity  ComponentRole = "security"
	RoleExtension ComponentRole = "extension"
	RoleUnknown   ComponentRole = "unknown"
)

// subordinateRoles are roles that fold into a parent app when one exists.
// Workloads with ONLY subordinate-role components don't render as top-level rows.
var subordinateRoles = map[ComponentRole]bool{
	RoleDatabase:  true,
	RoleCache:     true,
	RoleQueue:     true,
	RoleSearch:    true,
	RoleSentinel:  true,
	RoleSecurity:  true,
	RoleExtension: true,
	// NOT proxy — some proxies are the app itself
	// NOT worker — often primary business logic
}

// imageRolePatterns maps container image substrings (lowercase) to roles.
// Checked case-insensitively against the image name (without tag/registry).
var imageRolePatterns = []struct {
	pattern string
	role    ComponentRole
}{
	{"postgresql", RoleDatabase},
	{"postgres", RoleDatabase},
	{"mariadb", RoleDatabase},
	{"mysql", RoleDatabase},
	{"redis", RoleCache},
	{"memcached", RoleCache},
	{"elasticsearch", RoleSearch},
	{"opensearch", RoleSearch},
	{"rabbitmq", RoleQueue},
	{"nats", RoleQueue},
	{"kafka", RoleQueue},
	{"minio", RoleDatabase},
	{"sentinel", RoleSentinel},
	{"clamav", RoleSecurity},
}

// suffixRolePatterns maps workload name suffixes to roles.
// Ordered longest-first so `-mariadb` matches before `-db`.
var suffixRolePatterns = []struct {
	suffix string
	role   ComponentRole
}{
	// Database
	{"-postgresql", RoleDatabase},
	{"-postgres", RoleDatabase},
	{"-mariadb", RoleDatabase},
	{"-mysql", RoleDatabase},
	{"-db", RoleDatabase},
	// Cache
	{"-memcached", RoleCache},
	{"-redis", RoleCache},
	{"-cache", RoleCache},
	// Sentinel / HA
	{"-sentinel", RoleSentinel},
	// Search
	{"-elasticsearch", RoleSearch},
	{"-opensearch", RoleSearch},
	// Queue
	{"-rabbitmq", RoleQueue},
	{"-nats", RoleQueue},
	{"-queue", RoleQueue},
	{"-broker", RoleQueue},
	// Security
	{"-clamav", RoleSecurity},
}

// extensionSuffixes identify app-specific feature components that are
// subordinate but not backing services. These are features/plugins of a parent app.
var extensionSuffixes = []string{
	"-notify-push",
	"-talk-hpb",
	"-whiteboard",
}

// ClassifyComponentRole determines a workload's role from its container images
// and workload name. Image patterns take priority over name suffixes.
// Returns RoleUnknown if no signal matches (treated as non-subordinate).
func ClassifyComponentRole(workloadName string, imageNames []string) ComponentRole {
	nameLower := strings.ToLower(workloadName)

	// Signal 1: container image names (strongest — you ARE what you run)
	for _, img := range imageNames {
		imgLower := strings.ToLower(img)
		for _, p := range imageRolePatterns {
			if strings.Contains(imgLower, p.pattern) {
				return p.role
			}
		}
	}

	// Signal 2: workload name suffixes (naming conventions)
	for _, p := range suffixRolePatterns {
		if strings.HasSuffix(nameLower, p.suffix) {
			return p.role
		}
	}

	// Signal 3: extension suffixes (app features, not backing services)
	for _, s := range extensionSuffixes {
		if strings.HasSuffix(nameLower, s) {
			return RoleExtension
		}
	}

	return RoleUnknown
}

// IsSubordinate returns true if the role should fold into a parent app.
func IsSubordinate(role ComponentRole) bool {
	return subordinateRoles[role]
}

// FoldSubordinates merges subordinate-only app records into their parent records.
// A record is subordinate-only if ALL its components have subordinate roles.
// Parent matching: same namespace, subordinate identity starts with parent identity + "-".
// Longest matching parent identity wins (most specific).
// Unmatched subordinates stay as top-level rows (surfaces misconfiguration).
func FoldSubordinates(records []AppRecord) []AppRecord {
	// Partition into parents and subordinates.
	var parents, subordinates []AppRecord
	for _, r := range records {
		if isAllSubordinate(r) {
			subordinates = append(subordinates, r)
		} else {
			parents = append(parents, r)
		}
	}

	if len(subordinates) == 0 {
		return records
	}

	// Sort subordinates by identity for deterministic processing.
	sort.Slice(subordinates, func(i, j int) bool {
		if subordinates[i].Key.Namespace != subordinates[j].Key.Namespace {
			return subordinates[i].Key.Namespace < subordinates[j].Key.Namespace
		}
		return subordinates[i].Key.Identity < subordinates[j].Key.Identity
	})

	// Build parent index for matching.
	type parentEntry struct {
		idx      int
		identity string
	}
	parentsByNS := map[string][]parentEntry{}
	for i, p := range parents {
		parentsByNS[p.Key.Namespace] = append(parentsByNS[p.Key.Namespace],
			parentEntry{idx: i, identity: p.Key.Identity})
	}

	// Match subordinates to parents.
	var orphans []AppRecord
	for _, sub := range subordinates {
		candidates := parentsByNS[sub.Key.Namespace]
		bestIdx := -1
		bestLen := 0

		for _, c := range candidates {
			prefix := c.identity + "-"
			if strings.HasPrefix(sub.Key.Identity, prefix) && len(c.identity) > bestLen {
				bestIdx = c.idx
				bestLen = len(c.identity)
			}
		}

		if bestIdx >= 0 {
			// Merge subordinate's components into parent.
			parents[bestIdx].Components = append(parents[bestIdx].Components, sub.Components...)
		} else {
			orphans = append(orphans, sub)
		}
	}

	return append(parents, orphans...)
}

// isAllSubordinate returns true if every component in the record has a subordinate role.
func isAllSubordinate(r AppRecord) bool {
	if len(r.Components) == 0 {
		return false
	}
	for _, c := range r.Components {
		if !IsSubordinate(c.Role) {
			return false
		}
	}
	return true
}

// extractImageName returns the image name without registry prefix and tag.
// "docker.io/library/redis:7-alpine" → "redis"
// "ghcr.io/open-webui/open-webui:latest" → "open-webui"
func extractImageName(image string) string {
	// Strip tag/digest
	if idx := strings.LastIndex(image, ":"); idx > 0 {
		// Don't strip port from registry (e.g., localhost:5000/foo)
		afterColon := image[idx+1:]
		if !strings.Contains(afterColon, "/") {
			image = image[:idx]
		}
	}
	if idx := strings.LastIndex(image, "@"); idx > 0 {
		image = image[:idx]
	}
	// Take last path segment as the image name
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		image = image[idx+1:]
	}
	return image
}

// longestCommonHyphenPrefix computes the longest shared prefix among names,
// truncated to a full hyphen-segment boundary.
// ["open-webui-redis", "open-webui-sentinel"] → "open-webui"
// ["ark-sa-theisland", "ark-se-theisland"] → "ark"
// Returns "" if no common prefix or fewer than 2 names.
func longestCommonHyphenPrefix(names []string) string {
	if len(names) < 2 {
		return ""
	}

	// Start with full first name as candidate.
	prefix := names[0]
	for _, name := range names[1:] {
		// Shrink prefix to common characters.
		i := 0
		for i < len(prefix) && i < len(name) && prefix[i] == name[i] {
			i++
		}
		prefix = prefix[:i]
		if prefix == "" {
			return ""
		}
	}

	// Truncate to last hyphen boundary.
	if idx := strings.LastIndex(prefix, "-"); idx > 0 {
		return prefix[:idx]
	}

	// No hyphen in common prefix — not a valid family prefix.
	return ""
}
