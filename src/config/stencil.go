package config

import (
	"fmt"
	"path"

	"gopkg.in/yaml.v3"
)

// StencilDef is one entry in the top-level stencils: library — a named, reusable
// markdown element with variable fill, embeddable as {id} anywhere SF composes
// audience text (scribe file regions, narrate, release bodies). Its shape is SOURCE
// (type:) × RENDER (render:): an inline def (no type) renders a badge (default) or
// shield; a named producer (type: goreportcard, github-*, contents, include, text,
// component, k8s-inventory) renders through the matching module. The id (map key) is
// the {id} embed target and the default label.
type StencilDef struct {
	ID string `yaml:"-"` // set from the stencils map key

	// SOURCE × RENDER.
	Type   string `yaml:"type,omitempty"`   // producer; empty = inline
	Render string `yaml:"render,omitempty"` // form: badge (default) | shield | image | table | list | kv | versions | raw

	// ── inline badge / shield areas ──
	Label      string `yaml:"label,omitempty"`     // left text (defaults to id); also props alt override
	Message    string `yaml:"message,omitempty"`   // right value (templates)
	Color      string `yaml:"color,omitempty"`     // hex or "auto"
	Font       string `yaml:"font,omitempty"`      // badge font override
	FontSize   int    `yaml:"font_size,omitempty"` // badge font size override
	Output     string `yaml:"output,omitempty"`    // SVG output path (badge generation)
	Link       string `yaml:"link,omitempty"`      // clickable URL
	Logo       string `yaml:"logo,omitempty"`      // shields.io logo / props logo
	LogoColor  string `yaml:"logo_color,omitempty"`
	LabelColor string `yaml:"label_color,omitempty"`
	Shield     string `yaml:"shield,omitempty"` // shields.io path (render: shield)

	// ── text ──
	Content string `yaml:"content,omitempty"`

	// ── component ──
	Spec string `yaml:"spec,omitempty"`

	// ── include ──
	Path string `yaml:"path,omitempty"`

	// ── contents (build manifest) ──
	Build   string `yaml:"build,omitempty"`
	Source  string `yaml:"source,omitempty"`
	Section string `yaml:"section,omitempty"`
	// contents renderer form lives on the RENDER axis (render:), not a separate
	// renderer: key — one form vocabulary (badges|table|list|kv|versions).
	Columns    []string `yaml:"columns,omitempty"`
	OutputFile string   `yaml:"output_file,omitempty"`
	Wrap       string   `yaml:"wrap,omitempty"`
	Summary    string   `yaml:"summary,omitempty"`

	// ── named-producer coordinate override (props) ──
	Repo string `yaml:"repo,omitempty"` // repos: id whose coordinates the producer uses (default: publish-origin)

	// ── props (github-*, goreportcard, …) ──
	Params map[string]string `yaml:"params,omitempty"`
	Style  string            `yaml:"style,omitempty"`

	// ── k8s-inventory ──
	CatalogPath string `yaml:"catalog,omitempty"`
}

// EffectiveKind maps (Type, Render) to the render module used for this stencil. It is
// the discriminator the runtime dispatches on when resolving a {id} embed to markdown.
func (c StencilDef) EffectiveKind() string {
	switch c.Type {
	case "":
		if c.Render == "shield" {
			return "shield"
		}
		return "badge" // inline default
	case "contents":
		return "build-contents"
	case "include":
		return "include"
	case "text":
		return "text"
	case "component":
		return "component"
	case "k8s-inventory":
		return "k8s-inventory"
	default:
		return "props" // goreportcard, go-reference, github-*, star-history
	}
}

// LabelOrID returns the badge/alt label, defaulting to the stencil id.
func (c StencilDef) LabelOrID() string {
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

// HasGeneration reports whether this stencil produces an SVG badge (an inline badge
// with an output path).
func (c StencilDef) HasGeneration() bool {
	return c.EffectiveKind() == "badge" && c.Output != ""
}

// ToBadgeSpec extracts the SVG-generation fields.
func (c StencilDef) ToBadgeSpec() BadgeSpec {
	return BadgeSpec{
		Label:    c.LabelOrID(),
		Value:    c.Message,
		Color:    c.Color,
		Output:   c.Output,
		Font:     c.Font,
		FontSize: float64(c.FontSize),
	}
}

// OrderedStencils is the top-level stencils: library — id → StencilDef, document order.
type OrderedStencils []StencilDef

func (o *OrderedStencils) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(c *StencilDef, id string) { c.ID = id })
	if err != nil {
		return fmt.Errorf("stencils: %w", err)
	}
	*o = v
	return nil
}

// ByID returns a lookup map of the stencil library keyed by id.
func (o OrderedStencils) ByID() map[string]StencilDef {
	m := make(map[string]StencilDef, len(o))
	for _, s := range o {
		m[s.ID] = s
	}
	return m
}

// StencilsByID is the Config-level convenience over the stencil library.
func (c *Config) StencilsByID() map[string]StencilDef {
	return c.Stencils.ByID()
}

// ApplyStencilStoreDefaults derives Output = {store}/{id}.svg for badge stencils that
// don't set an explicit output path, so a badge stencil needs no per-item output:
// line. An explicit output: wins. Store comes from scribe.store (default
// DefaultScribeStore) — badge SVGs are a scribe-placed asset.
func (c *Config) ApplyStencilStoreDefaults() {
	store := c.Scribe.Store
	if store == "" {
		store = DefaultScribeStore
	}
	for i := range c.Stencils {
		s := &c.Stencils[i]
		if s.Output == "" && s.EffectiveKind() == "badge" {
			s.Output = path.Join(store, s.ID+".svg")
		}
	}
}

// reservedStencilIDs are gitver leaf-fact keywords; a stencil id must not shadow one,
// or `{id}` would be consumed by the gitver leaf-pass instead of the stencil.
var reservedStencilIDs = map[string]bool{
	"version": true, "base": true, "major": true, "minor": true, "patch": true,
	"prerelease": true, "branch": true, "sha": true, "date": true, "datetime": true,
	"timestamp": true, "n": true, "hex": true, "rand": true, "randhex": true,
}
