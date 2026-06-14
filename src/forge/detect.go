package forge

import (
	"fmt"
	"strings"
	"time"
)

// DetectProvider determines the forge platform from a git remote URL.
func DetectProvider(remoteURL string) Provider {
	lower := strings.ToLower(remoteURL)

	switch {
	case strings.Contains(lower, "github.com"):
		return GitHub
	case strings.Contains(lower, "dev.azure.com") || strings.Contains(lower, "visualstudio.com"):
		return AzureDevOps
	case strings.Contains(lower, "gitlab"):
		return GitLab
	// Codeberg runs Forgejo; treat forgejo/codeberg hosts as first-class Forgejo.
	case strings.Contains(lower, "forgejo") || strings.Contains(lower, "codeberg"):
		return Forgejo
	case strings.Contains(lower, "gitea"):
		return Gitea
	default:
		// Self-hosted instances without obvious domain hints.
		// Future: probe the API to detect (GitLab /api/v4, GitHub /api/v3,
		// Gitea/Forgejo /api/v1 + the server's version banner).
		return Unknown
	}
}

// ParseProvider maps a DECLARED provider string (case-insensitive) to a known
// Provider, or Unknown when unrecognized. Unlike DetectProvider — which guesses the
// provider from a transport URL — this is the authoritative path: the operator has
// stated the forge type in config, so it is trusted over any URL heuristic. The git
// remote is transport identity (how to reach the repo); the provider is forge identity
// (which API to speak), and the two are not coupled (proxies, SSH aliases, IP remotes).
func ParseProvider(s string) Provider {
	switch Provider(strings.ToLower(strings.TrimSpace(s))) {
	case GitLab:
		return GitLab
	case GitHub:
		return GitHub
	case Gitea:
		return Gitea
	case Forgejo:
		return Forgejo
	case AzureDevOps:
		return AzureDevOps
	default:
		return Unknown
	}
}

// BaseURL extracts the forge base URL from a git remote URL.
// Handles SSH (git@host:path) and HTTPS (https://host/path) formats.
func BaseURL(remoteURL string) string {
	url := remoteURL

	// SSH format: git@host:org/repo.git
	if strings.HasPrefix(url, "git@") || strings.Contains(url, "@") && strings.Contains(url, ":") {
		// git@host:path → https://host
		parts := strings.SplitN(url, "@", 2)
		if len(parts) == 2 {
			hostPath := parts[1]
			// Handle SSH port: git@host:port:path or ssh://git@host:port/path
			colonIdx := strings.Index(hostPath, ":")
			if colonIdx >= 0 {
				host := hostPath[:colonIdx]
				return "https://" + host
			}
		}
	}

	// HTTPS format: https://host/org/repo.git
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		// Strip path to get base URL
		withoutScheme := url
		scheme := "https://"
		if strings.HasPrefix(url, "http://") {
			scheme = "http://"
			withoutScheme = strings.TrimPrefix(url, "http://")
		} else {
			withoutScheme = strings.TrimPrefix(url, "https://")
		}
		slashIdx := strings.Index(withoutScheme, "/")
		if slashIdx >= 0 {
			return scheme + withoutScheme[:slashIdx]
		}
		return scheme + withoutScheme
	}

	return url
}

// parseTime tries common timestamp formats returned by forge APIs.
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.000-07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized time format: %q", s)
}
