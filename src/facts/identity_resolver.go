package facts

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// identityResolver resolves the config-sourced identity families — {org.*},
// {orgs.<id>.*}, and {metadata.*} — from the orgs: and metadata: sections. The current
// repo's org is metadata.org (a ref into orgs); {org.<alias>} reads that org's aliases.
// Coordinates ({path.<surface>}, {slug}) are a separate resolver (they also need the
// surface default_paths). Tokens this resolver doesn't own are left untouched for later
// resolvers (gitver leaf, registry, inventory).
type identityResolver struct{}

// IdentityResolver returns the resolver for the {org.*}/{orgs.*}/{metadata.*} families.
func IdentityResolver() Resolver { return identityResolver{} }

func (identityResolver) Name() string        { return "identity" }
func (identityResolver) Provides() []string  { return []string{"org", "orgs", "metadata", "path", "slug"} }
func (identityResolver) DependsOn() []string { return nil }

func (identityResolver) Resolve(values []string, c *Context) []string {
	if c == nil || c.Config == nil {
		return values
	}
	subs := identitySubs(c.Config, c.RootDir)
	if len(subs) == 0 {
		return values
	}
	out := make([]string, len(values))
	for i, v := range values {
		s := v
		if strings.Contains(s, "{org") || strings.Contains(s, "{metadata.") ||
			strings.Contains(s, "{path.") || strings.Contains(s, "{slug}") {
			for tok, val := range subs {
				if strings.Contains(s, tok) {
					s = strings.ReplaceAll(s, tok, val)
				}
			}
		}
		out[i] = s
	}
	return out
}

// identitySubs builds the exact brace-delimited token → value map from config. Tokens
// are brace-delimited so none is a substring of another as a whole token (e.g. "{org}"
// never matches inside "{org.lower}"), making ReplaceAll order-independent. Only tokens
// the config can populate are added; a typo'd token has no entry and stays literal.
func identitySubs(cfg *config.Config, rootDir string) map[string]string {
	subs := map[string]string{}

	// {org.*} — the current repo's org (metadata.org ref).
	if org, ok := cfg.Orgs.ByID(cfg.Metadata.Org); ok {
		addOrgSubs(subs, "org", org)
	}
	// {orgs.<id>.*} — any org by id (for cross-org references).
	for _, org := range cfg.Orgs {
		addOrgSubs(subs, "orgs."+org.ID, org)
	}

	// {metadata.*} — this repo's branding. These are a CLOSED set of known fields, so
	// each always resolves (empty → "", matching {project.*}); only a typo'd, non-field
	// token stays literal. {metadata.description} is the default's shortest tier (the
	// fit-picked short form); publish uses the full scoped value. labels are OPEN, so
	// only declared keys are added.
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

	// {slug} + {path.<surface>} — coordinates. slug is the primary repo's name; a
	// surface's path is its default_path resolved with the current org's {org.*} aliases
	// and {repo} = names[surface] ?? slug. Forges and registries both carry default_path.
	// Any {var:}/etc. left in a default_path is resolved by the later gitver leaf pass.
	slug := currentSlug(cfg, rootDir)
	if slug != "" {
		subs["{slug}"] = slug
	}
	for id, defaultPath := range surfacePaths(cfg) {
		if defaultPath == "" {
			continue
		}
		repo := m.Names[id]
		if repo == "" {
			repo = slug
		}
		p := defaultPath
		for tok, val := range subs {
			if tok == "{org}" || strings.HasPrefix(tok, "{org.") {
				p = strings.ReplaceAll(p, tok, val)
			}
		}
		p = strings.ReplaceAll(p, "{repo}", repo)
		subs["{path."+id+"}"] = p
	}
	return subs
}

// currentSlug is the repo's name that coordinates default {repo} to. It prefers a
// LITERAL primary repo location's last segment (the settled anchor). A templated primary
// project (e.g. "{path.gitlab}") can't anchor the slug — its last segment is garbage and
// deriving the slug from a {path.*} it would itself feed is circular — so that case (and
// a missing primary) falls back to the git-detected repo name from the remote.
func currentSlug(cfg *config.Config, rootDir string) string {
	for _, r := range cfg.Repos {
		if r.HasRole("primary") && !strings.Contains(r.Project, "{") {
			return lastSegment(r.Project)
		}
	}
	if rootDir != "" {
		if pm := gitver.DetectProject(rootDir); pm != nil && pm.Name != "" {
			return pm.Name
		}
	}
	return ""
}

func lastSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// surfacePaths maps every forge and registry id to its default_path template. Forge and
// registry ids are required to be disjoint (validated), so a key never collides.
func surfacePaths(cfg *config.Config) map[string]string {
	out := make(map[string]string, len(cfg.Forges)+len(cfg.Registries))
	for _, f := range cfg.Forges {
		out[f.ID] = f.DefaultPath
	}
	for _, r := range cfg.Registries {
		out[r.ID] = r.DefaultPath
	}
	return out
}

// addOrgSubs adds {<prefix>}, {<prefix>.lower}, {<prefix>.maintainer}, and one
// {<prefix>.<alias>} per declared alias, for prefix "org" (current) or "orgs.<id>".
// Gated on the org existing, so {org.*} for an absent org stays literal rather than
// resolving to "" (aliases are open — undeclared {org.<x>} can't be enumerated to blank).
func addOrgSubs(subs map[string]string, prefix string, org config.OrgConfig) {
	subs["{"+prefix+"}"] = org.ID
	subs["{"+prefix+".lower}"] = strings.ToLower(org.ID)
	subs["{"+prefix+".maintainer}"] = org.Maintainer
	for k, v := range org.Aliases {
		subs["{"+prefix+"."+k+"}"] = v
	}
}
