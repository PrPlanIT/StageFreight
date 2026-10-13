package k8s

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PrPlanIT/StageFreight/src/config"
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
	Tier         string       `yaml:"tier"`   // "app", "platform", "hidden"
	Ignore       bool         `yaml:"ignore"` // suppress from output entirely
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

// Uncategorized is the section for namespaces the cluster declares no category for.
const Uncategorized = "Uncategorized"

// CategoryResolver answers what the cluster declares about a namespace: its inventory
// section, that section's position, and the tier of everything in it.
type CategoryResolver struct {
	categories  []config.InventoryCategory
	byNamespace map[string]int
	order       map[string]int
}

func NewCategoryResolver(categories []config.InventoryCategory) *CategoryResolver {
	r := &CategoryResolver{categories: categories, byNamespace: map[string]int{}, order: map[string]int{}}
	for i, c := range categories {
		r.order[c.Name] = i
		for _, ns := range c.Namespaces {
			r.byNamespace[ns] = i
		}
	}
	return r
}

// Resolve returns the namespace's declared category, or Uncategorized.
func (r *CategoryResolver) Resolve(namespace string) string {
	if i, ok := r.byNamespace[namespace]; ok {
		return r.categories[i].Name
	}
	return Uncategorized
}

// Declared reports whether the cluster places the namespace in a category.
func (r *CategoryResolver) Declared(namespace string) bool {
	_, ok := r.byNamespace[namespace]
	return ok
}

// Tier returns the namespace's declared tier, or "" when the cluster declares none.
func (r *CategoryResolver) Tier(namespace string) Tier {
	if i, ok := r.byNamespace[namespace]; ok {
		return Tier(r.categories[i].Tier)
	}
	return ""
}

// Order ranks a category by its declared position; undeclared categories sort last.
func (r *CategoryResolver) Order(category string) int {
	if i, ok := r.order[category]; ok {
		return i
	}
	return len(r.categories)
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
