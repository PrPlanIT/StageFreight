package config

import (
	"fmt"
	"path"
	"strings"

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

	// ── text (the dumb markdown stencil: facts + {id} stencil embeds, no logic) ──
	// llm shares body: — there it is the composed INPUT (inject {ci-detail} etc.
	// and write the ask inline; no separate prompt slot).
	Body string `yaml:"body,omitempty"`

	// ── llm (AI stencil: composed body × llms: backend → markdown) ──
	LLM string `yaml:"llm,omitempty"` // llms: entry id (the backend reference)

	// ── component ──
	Spec string `yaml:"spec,omitempty"`

	// ── include ──
	Path string `yaml:"path,omitempty"`

	// ── contents (build manifest) / ci (run-state producers) ──
	Build   string `yaml:"build,omitempty"`
	Source  string `yaml:"source,omitempty"`
	Section string `yaml:"section,omitempty"`
	// Limit caps a ci producer's rows (self-bounding: excess becomes "+K more").
	Limit int `yaml:"limit,omitempty"`
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
	case "ci":
		return "ci"
	case "llm":
		return "llm"
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

// reservedStencilIDs are reserved fact names; a stencil id must not shadow one, or
// `{id}` would be consumed by a fact resolver instead of the stencil. The set is the
// gitver leaf-fact keywords plus the bare run-status facts (cistate).
var reservedStencilIDs = map[string]bool{
	"version": true, "base": true, "major": true, "minor": true, "patch": true,
	"prerelease": true, "branch": true, "sha": true, "date": true, "datetime": true,
	"timestamp": true, "n": true, "hex": true, "rand": true, "randhex": true,
	"status": true, "status_icon": true, "status_verb": true,
}

// findStencilCycle detects an embed cycle among declared stencils: edges are {id}
// tokens in a stencil's body that name another DECLARED stencil (every other token is
// a fact/var for the leaf-pass). Returns a printable "a → b → a" path, or "" if the
// graph is acyclic. Deterministic: starts DFS in document order.
func findStencilCycle(stencils OrderedStencils, declared map[string]bool) string {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	byID := stencils.ByID()
	state := make(map[string]int, len(stencils))

	var path []string
	var walk func(id string) string
	walk = func(id string) string {
		state[id] = visiting
		path = append(path, id)
		for _, ref := range BodyRefs(byID[id].Body) {
			if !declared[ref] {
				continue
			}
			switch state[ref] {
			case visiting:
				// Trim the path to the cycle entry point for a readable report.
				start := 0
				for i, p := range path {
					if p == ref {
						start = i
						break
					}
				}
				return strings.Join(append(path[start:], ref), " → ")
			case unvisited:
				if cycle := walk(ref); cycle != "" {
					return cycle
				}
			}
		}
		path = path[:len(path)-1]
		state[id] = done
		return ""
	}

	for _, s := range stencils {
		if state[s.ID] == unvisited {
			if cycle := walk(s.ID); cycle != "" {
				return cycle
			}
		}
	}
	return ""
}

// BodyRefs scans a body for {name} embed tokens (skipping {{…}} literal escapes) and
// returns the names in order of appearance. It mirrors the stencil engine's brace scan
// so validation (cycle detection) and rendering agree on what a body references.
func BodyRefs(body string) []string {
	var refs []string
	s := body
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return refs
		}
		s = s[open:]
		if strings.HasPrefix(s, "{{") {
			s = s[2:]
			continue
		}
		close := strings.IndexByte(s, '}')
		if close < 0 {
			return refs
		}
		name := strings.TrimSpace(s[1:close])
		if name != "" {
			refs = append(refs, name)
		}
		s = s[close+1:]
	}
}
