package config

import "strings"

// Identity resolution is a NORMALIZATION concern: every identity fact — {org.*},
// {orgs.<id>.*}, {metadata.*}, {slug}, {path.<surface>} — is a pure function of the
// loaded config, so it resolves once at load, in the same pass family as {var:}. Every
// consumer (push refs, forge API calls, pages, badges, scribe, cache paths) then sees
// concrete strings from ONE model; nothing resolves identity again downstream.
//
// The governance payload is opaque here exactly as it is for preset resolution: a
// profile's config: describes the SATELLITE, whose identity resolves at the satellite's
// own load — the control repo never expands it.

// IdentitySubs builds the brace-delimited token → value map from config. Tokens are
// brace-delimited so none is a substring of another as a whole token (e.g. "{org}"
// never matches inside "{org.lower}"), making ReplaceAll order-independent. Only tokens
// the config can populate are added; a typo'd token has no entry, stays literal, and
// the normalization assertion rejects it loudly.
func IdentitySubs(cfg *Config) map[string]string {
	subs := map[string]string{}

	// {org.*} — the current repo's org (metadata.org ref).
	if org, ok := cfg.Orgs.ByID(cfg.Metadata.Org); ok {
		addOrgSubs(subs, "org", org)
	}
	// {orgs.<id>.*} — any org by id (for cross-org references).
	for _, org := range cfg.Orgs {
		addOrgSubs(subs, "orgs."+org.ID, org)
	}

	// {metadata.*} — this repo's branding. A CLOSED set of known fields, so each always
	// resolves (empty → "", matching {project.*}); only a typo'd, non-field token stays
	// literal. {metadata.description} is the default's shortest tier. Labels are OPEN,
	// so only declared keys are added.
	m := cfg.Metadata
	subs["{metadata.title}"] = m.Title
	subs["{metadata.license}"] = m.License
	subs["{metadata.category}"] = m.Category
	subs["{metadata.website}"] = m.Website
	subs["{metadata.docs_url}"] = m.DocsURL
	subs["{metadata.icon}"] = m.Icon
	subs["{metadata.description}"] = m.Description.Default.First()
	for k, v := range m.Labels {
		subs["{metadata.labels."+k+"}"] = v
	}

	// {slug} — the primary repo location's last segment. The location is the settled
	// authoritative anchor (native repos declare it; governance concretizes it from the
	// catalog entry), so a templated primary simply yields no {slug} sub and the
	// assertion names the problem.
	slug := SlugFromPrimary(cfg)
	if slug != "" {
		subs["{slug}"] = slug
	}
	return subs
}

// SlugFromPrimary is the repo's name that coordinates default {repo} to: the LITERAL
// primary repo location's last segment. "" when no primary exists or its project is
// still templated (which cannot anchor a slug).
func SlugFromPrimary(cfg *Config) string {
	for _, r := range cfg.Repos {
		if r.HasRole("primary") && !strings.Contains(r.Project, "{") {
			return lastPathSegment(r.Project)
		}
	}
	return ""
}

func lastPathSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// expandIdentity is the normalization pass: it concretizes every surface default_path
// ({org.*} aliases + {repo} = metadata.names[surface] ?? slug), registers the resulting
// {path.<surface>} coordinates, then rewrites every string in the config graph — with
// the governance payload skipped (opaque, satellite-owned).
func expandIdentity(cfg *Config) {
	subs := IdentitySubs(cfg)
	slug := subs["{slug}"]

	// Concretize default_paths in place, then expose them as {path.<id>}. Forge and
	// registry ids are disjoint (validated), so keys never collide.
	concretize := func(id, defaultPath string) string {
		if defaultPath == "" {
			return defaultPath
		}
		p := defaultPath
		for tok, val := range subs {
			if tok == "{org}" || strings.HasPrefix(tok, "{org.") || strings.HasPrefix(tok, "{orgs.") {
				p = strings.ReplaceAll(p, tok, val)
			}
		}
		repo := cfg.Metadata.Names[id]
		if repo == "" {
			repo = slug
		}
		if repo != "" {
			p = strings.ReplaceAll(p, "{repo}", repo)
		}
		return p
	}
	for i := range cfg.Forges {
		cfg.Forges[i].DefaultPath = concretize(cfg.Forges[i].ID, cfg.Forges[i].DefaultPath)
		if cfg.Forges[i].DefaultPath != "" && !strings.Contains(cfg.Forges[i].DefaultPath, "{") {
			subs["{path."+cfg.Forges[i].ID+"}"] = cfg.Forges[i].DefaultPath
		}
	}
	for i := range cfg.Registries {
		cfg.Registries[i].DefaultPath = concretize(cfg.Registries[i].ID, cfg.Registries[i].DefaultPath)
		if cfg.Registries[i].DefaultPath != "" && !strings.Contains(cfg.Registries[i].DefaultPath, "{") {
			subs["{path."+cfg.Registries[i].ID+"}"] = cfg.Registries[i].DefaultPath
		}
	}

	replace := func(s string) string {
		if !strings.Contains(s, "{") {
			return s
		}
		for tok, val := range subs {
			if strings.Contains(s, tok) {
				s = strings.ReplaceAll(s, tok, val)
			}
		}
		return s
	}
	walkStrings(cfg, replace)
}

// identityFamilies are the token prefixes the identity pass owns; the normalization
// assertion rejects any survivor outside the (opaque) governance payload.
var identityFamilies = []string{"{org}", "{org.", "{orgs.", "{metadata.", "{path.", "{slug}"}

func addOrgSubs(subs map[string]string, prefix string, org OrgConfig) {
	subs["{"+prefix+"}"] = org.ID
	subs["{"+prefix+".lower}"] = strings.ToLower(org.ID)
	subs["{"+prefix+".maintainer}"] = org.Maintainer
	for k, v := range org.Aliases {
		subs["{"+prefix+"."+k+"}"] = v
	}
}
