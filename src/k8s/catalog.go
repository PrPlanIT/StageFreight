package k8s

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Catalog holds human-curated metadata that augments cluster discovery.
// Cluster tells you what exists; catalog tells you how to describe it.
// Catalog is for exceptions only — exposure rules live on gitops.cluster.
type Catalog struct {
	Apps      []CatalogApp     `yaml:"apps"`
	Graveyard []GraveyardEntry `yaml:"graveyard"`
}

// CatalogApp defines metadata overrides for a discovered application.
type CatalogApp struct {
	Match        CatalogMatch `yaml:"match"`
	FriendlyName string       `yaml:"friendly_name"`
	Description  string       `yaml:"description"`
	Tier         string       `yaml:"tier"`     // "app", "platform", "hidden"
	Ignore       bool         `yaml:"ignore"`   // suppress from output entirely
	HomepageURL  string       `yaml:"homepage_url"`
	DocsURL      string       `yaml:"docs_url"`
	SourceURL    string       `yaml:"source_url"`
}

// CatalogMatch identifies which discovered app this entry applies to.
type CatalogMatch struct {
	Namespace string `yaml:"namespace"`
	Identity  string `yaml:"identity"`
}

// catalogFile is the top-level YAML structure of .stagefreight-catalog.yml.
type catalogFile struct {
	Catalog Catalog `yaml:"catalog"`
}

// LoadCatalog reads and parses a catalog metadata file.
// Returns an empty catalog if the file does not exist.
func LoadCatalog(path string) (*Catalog, error) {
	if path == "" {
		return &Catalog{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Catalog{}, nil
		}
		return nil, fmt.Errorf("reading catalog %s: %w", path, err)
	}

	var cf catalogFile
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parsing catalog %s: %w", path, err)
	}

	return &cf.Catalog, nil
}

// LookupApp finds catalog metadata for a given app key.
// Returns nil if no match found.
func (c *Catalog) LookupApp(key AppKey) *CatalogApp {
	for i := range c.Apps {
		m := c.Apps[i].Match
		if m.Namespace == key.Namespace && m.Identity == key.Identity {
			return &c.Apps[i]
		}
	}
	return nil
}

// ApplyOverrides merges catalog metadata onto an AppRecord.
// Catalog values take precedence over discovery for description,
// friendly name, tier, and URLs.
func (c *Catalog) ApplyOverrides(rec *AppRecord) {
	entry := c.LookupApp(rec.Key)
	if entry == nil {
		return
	}

	if entry.Ignore {
		rec.Tier = TierHidden
		return
	}

	if entry.FriendlyName != "" {
		rec.FriendlyName = entry.FriendlyName
	}
	if entry.Description != "" {
		rec.Description = entry.Description
	}
	if entry.Tier != "" {
		rec.Tier = Tier(entry.Tier)
	}
	if entry.HomepageURL != "" {
		rec.HomepageURL = entry.HomepageURL
	}
	if entry.DocsURL != "" {
		rec.DocsURL = entry.DocsURL
	}
	if entry.SourceURL != "" {
		rec.SourceURL = entry.SourceURL
	}
}

// CategoryResolver maps namespaces to human-readable category names.
type CategoryResolver struct {
	overrides map[string]string
}

// defaultCategories maps Zelda-themed namespaces to category names.
var defaultCategories = map[string]string{
	"temple-of-time":    "Archival & Media",
	"swift-sail":        "Downloads & Arr",
	"gossip-stone":      "Monitoring",
	"lost-woods":        "Discovery & Dashboards",
	"shooting-gallery":  "Game Servers",
	"tingle-tuner":      "Tools & Utilities",
	"zeldas-lullaby":    "Administrative",
	"compass":           "DNS & NTP",
	"hookshot":          "Remote Management",
	"lens-of-truth":     "Security & IDS",
	"delivery-bag":      "Mail Services",
	"fairy-bottle":      "Backup Services",
	"gorons-bracelet":   "Storage Services",
	"wallmaster":        "Bot Protection",
	"gerudo-crest":      "Internal Trust",
	"pedestal-of-time":  "Privileged Services",
	"kokiri-forest":     "Personal Gateway",
	"hyrule-castle":     "Business Gateway",
	"arylls-lookout":    "Internal Gateway",
	"king-of-red-lions": "Routing Infrastructure",
	"kube-system":       "Platform",
	"flux-system":       "Platform",
	"istio-system":      "Platform",
	"cert-manager":      "Platform",
}

// NewCategoryResolver creates a resolver with built-in defaults and optional overrides.
func NewCategoryResolver(overrides map[string]string) *CategoryResolver {
	return &CategoryResolver{overrides: overrides}
}

// Resolve returns the category for a namespace.
// Precedence: overrides → defaults → "Uncategorized".
func (r *CategoryResolver) Resolve(namespace string) string {
	if r.overrides != nil {
		if cat, ok := r.overrides[namespace]; ok {
			return cat
		}
	}
	if cat, ok := defaultCategories[namespace]; ok {
		return cat
	}
	return "Uncategorized"
}

// CategoryOrder defines the stable rendering order for categories.
// Categories not in this list sort alphabetically after the listed ones.
var CategoryOrder = []string{
	"Administrative",
	"DNS & NTP",
	"Routing Infrastructure",
	"Storage Services",
	"Backup Services",
	"Monitoring",
	"Security & IDS",
	"Bot Protection",
	"Internal Trust",
	"Business Gateway",
	"Personal Gateway",
	"Internal Gateway",
	"Archival & Media",
	"Discovery & Dashboards",
	"Downloads & Arr",
	"Game Servers",
	"Mail Services",
	"Remote Management",
	"Tools & Utilities",
	"Privileged Services",
	"Platform",
	"Uncategorized",
}

// IsSidecarImage returns true if the image string matches a known sidecar pattern.
func IsSidecarImage(image string) bool {
	lower := strings.ToLower(image)
	for _, s := range SidecarImages {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}
