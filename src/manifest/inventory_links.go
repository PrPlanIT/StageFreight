package manifest

import (
	"net/url"
	"strings"
)

// inventoryItemLink derives a "where does this come from" web link for an
// inventory badge, from the item's manager and identity. Base images resolve to
// their registry's web UI (registry taken from the FROM ref — never assumed);
// package managers resolve to their ecosystem's package page. Returns "" when no
// public page is derivable (e.g. a private registry), so the badge renders unlinked.
func inventoryItemLink(item map[string]interface{}) string {
	manager := formatCell(item["manager"])
	name := formatCell(item["name"])
	switch manager {
	case "base":
		return baseImageWebURL(formatCell(item["source_ref"]))
	case "apk":
		if name == "" {
			return ""
		}
		return "https://pkgs.alpinelinux.org/packages?name=" + url.QueryEscape(name)
	case "apt":
		if name == "" {
			return ""
		}
		return "https://packages.debian.org/" + url.PathEscape(name)
	case "pip":
		if name == "" {
			return ""
		}
		return "https://pypi.org/project/" + url.PathEscape(name) + "/"
	case "npm":
		if name == "" {
			return ""
		}
		// npm package names (incl. @scope/pkg) are valid URL path segments as-is.
		return "https://www.npmjs.com/package/" + name
	case "go":
		if name == "" {
			return ""
		}
		return "https://pkg.go.dev/" + name
	case "galaxy":
		// name is "namespace.collection".
		if ns, coll, ok := strings.Cut(name, "."); ok && ns != "" && coll != "" {
			return "https://galaxy.ansible.com/ui/repo/published/" + ns + "/" + coll + "/"
		}
		return ""
	case "binary":
		return formatCell(item["url"])
	}
	return ""
}

// baseImageWebURL turns a "FROM <ref>" source ref into the registry's web page for
// that image. The registry host is read from the ref itself; a bare name defaults to
// Docker Hub (as Docker resolves it). Unknown/private registries return "".
func baseImageWebURL(sourceRef string) string {
	ref := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sourceRef), "FROM "))
	// Drop a leading --platform=... flag.
	for strings.HasPrefix(ref, "--") {
		_, rest, ok := strings.Cut(ref, " ")
		if !ok {
			return ""
		}
		ref = strings.TrimSpace(rest)
	}
	if f := strings.Fields(ref); len(f) > 0 {
		ref = f[0] // the image ref, before any "AS <stage>"
	} else {
		return ""
	}
	// Strip a digest pin, then a tag (a ":" after the last "/").
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	if ref == "" || ref == "scratch" {
		return ""
	}

	// Separate an explicit registry host (contains "." or ":", or is localhost)
	// from the repository path; a bare first segment means Docker Hub.
	registry, path := "docker.io", ref
	if first, rest, ok := strings.Cut(ref, "/"); ok {
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			registry, path = first, rest
		}
	}

	switch registry {
	case "docker.io":
		path = strings.TrimPrefix(path, "library/")
		if !strings.Contains(path, "/") {
			return "https://hub.docker.com/_/" + path // official library image
		}
		return "https://hub.docker.com/r/" + path
	case "ghcr.io":
		if owner, repo, ok := strings.Cut(path, "/"); ok {
			return "https://github.com/" + owner + "/" + repo + "/pkgs/container/" + lastPathSegment(repo)
		}
		return ""
	case "quay.io":
		return "https://quay.io/repository/" + path
	default:
		return "" // private/unknown registry — no public web UI to link
	}
}

func lastPathSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
