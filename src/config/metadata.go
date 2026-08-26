package config

// MetadataConfig is the metadata: block — a repo's identity/branding, the one place
// every consumer reads. The load-bearing fields (org, license, names) feed
// coordinates/module/license; the rest is branding, distributed for free. Feeds the
// {metadata.*} facts. See docs/design/identity-model.md.
//
// This is a single block (the CURRENT repo's identity). The governance catalog's per-
// repo entries reuse the same shape, keyed by id — that lands with the governance work.
type MetadataConfig struct {
	// Org references an orgs: entry by id (the repo's owner). May be derived from the
	// primary repo location instead of declared.
	Org string `yaml:"org,omitempty"`

	// Title is the human display name (e.g. "ARK Server"); defaults to the slug.
	Title string `yaml:"title,omitempty"`

	// Names overrides the repo's PATH name per surface (default = slug), e.g.
	// { dockerhub: arkserver, github: SteamCMD }.
	Names map[string]string `yaml:"names,omitempty"`

	// Description is scoped: the default may be a tiered StringOrList (fit-picked across
	// un-named surfaces); a named surface takes a single string.
	Description Scoped[StringOrList] `yaml:"description,omitempty"`

	// Readme is the source doc pushed to registry overviews / forge About, scoped:
	// a default path or per-surface paths. Defaults to the auto-detected README.
	Readme Scoped[string] `yaml:"readme,omitempty"`

	Topics   []string `yaml:"topics,omitempty"`
	License  string   `yaml:"license,omitempty"`
	Category string   `yaml:"category,omitempty"`

	// Website / DocsURL are optional and only for a genuinely SEPARATE site (product
	// page / hosted docs) — never the repo URL, which is a coordinate ({path.<forge>}).
	Website string `yaml:"website,omitempty"`
	DocsURL string `yaml:"docs_url,omitempty"`
	Icon    string `yaml:"icon,omitempty"`

	// Labels is the open forward-compat escape hatch (OCI-annotation style) for
	// metadata the schema does not model — funding, support/changelog URLs, deprecation.
	// It is NEVER a back door for a typed field above.
	Labels map[string]string `yaml:"labels,omitempty"`
}
