package config

import (
	"net/url"
	"strings"
)

// HostOf extracts the lowercased hostname (no scheme, no port, no path, no userinfo) from
// a URL or bare host string, for EXACT host matching. "https://gitlab.example.com:443/o/r"
// and "gitlab.example.com" both normalize to "gitlab.example.com". Returns "" when the
// input has no resolvable host. This is the single host-identity primitive used to bind a
// git credential to the forge that owns the destination host.
func HostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// ForgeForURL returns the configured forge whose host OWNS remoteURL's host, or nil when
// no configured forge matches. Matching is EXACT on the normalized hostname — never a
// prefix or substring compare — so a credential can only ever be selected for a host its
// forge actually claims. A forge URL's {var:...} placeholders resolve first. A nil result
// means "no configured forge owns this host": the caller must fall back to anonymous, so a
// token is never sent to an unclaimed host.
func ForgeForURL(remoteURL string, forges []ForgeConfig, vars map[string]string) *ForgeConfig {
	host := HostOf(remoteURL)
	if host == "" {
		return nil
	}
	for i := range forges {
		if HostOf(resolveVarsInline(forges[i].URL, vars)) == host {
			return &forges[i]
		}
	}
	return nil
}
