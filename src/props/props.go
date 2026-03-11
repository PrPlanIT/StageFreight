// Package props implements the composable presentation subsystem.
//
// Props provides typed, discoverable, validated, schema-aware presentation
// items. Users declare presentation items in config; StageFreight resolves
// them through typed, validated, schema-aware resolvers.
//
// Two-level dispatch: format (how it renders) + type (which resolver).
// Badges are the first prop format; the shape accommodates future formats.
package props

// ResolvedProp is the normalized output from any resolver — structured data, not markdown.
type ResolvedProp struct {
	ImageURL string
	LinkURL  string
	Alt      string // human-readable, e.g. "Docker Pulls", "Go Report Card"
}

// RenderOptions are presentation overrides supplied by narrator items.
// Assembled from narrator fields (label, link, style, logo) before resolution.
type RenderOptions struct {
	Label   string  // override Alt text
	Link    string  // override LinkURL
	Style   string  // shields.io style (flat, flat-square, etc.)
	Logo    string  // shields.io logo name
	Variant Variant // render variant — default: classic
}

// ParamSpec describes a single parameter for docs and validation.
type ParamSpec struct {
	Name     string
	Required bool
	Default  string // empty = no default
	Help     string // shown in `props show`
}

// PropSchema exposes docs/validation metadata for a resolver.
type PropSchema struct {
	Params  []ParamSpec
	Example map[string]string // example params for `props show`
}

// Provider source — lightweight metadata for docs/list output, not routing.
type Provider string

const (
	ProviderShields Provider = "shields" // img.shields.io
	ProviderNative  Provider = "native"  // service's own badge URL
	ProviderStatic  Provider = "static"  // fixed image, no dynamic params
)

// PropResolver is the core interface every resolver implements.
type PropResolver interface {
	Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error)
	Schema() PropSchema
}

// Definition is the registry entry for a prop type.
type Definition struct {
	ID          string
	Format      string       // "badge" (future: "image", "callout", etc.)
	Category    string       // grouping for list/docs (e.g. "docker", "security")
	Description string
	Provider    Provider
	DefaultAlt  string       // human-readable name — single source of truth
	Resolver    PropResolver
}
