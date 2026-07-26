package config

import (
	"fmt"
	"path"

	"gopkg.in/yaml.v3"
)

// DefaultScribeStore is the default directory for scribe-rendered file assets (badge
// SVGs, and any future rendered files). Named after the subsystem, consistent with
// .stagefreight/security, .stagefreight/deps, .stagefreight/manifests.
const DefaultScribeStore = ".stagefreight/scribe"

// ScribeConfig is the scribe: block — generate content into files and commit it.
// Content is DEFINED ONCE in content: (id → def) and REFERENCED by name in files:
// (placement + item name-refs). This replaces the old narrate: content surface
// (badges list + patches with inline items). narrate: is reserved for the report.
type ScribeConfig struct {
	Store   string         `yaml:"store,omitempty"`   // dir for rendered file assets (default .stagefreight/scribe); path = {store}/{id}.svg
	Content OrderedContent `yaml:"content,omitempty"` // id → content def (SOURCE type × RENDER render)
	Files   OrderedFiles   `yaml:"files,omitempty"`   // id → placement region, items are content name-refs
	Commit  ScribeCommit   `yaml:"commit,omitempty"`  // scribe's own auto-commit action
}

// ApplyStoreDefaults derives Output = {store}/{id}.svg for badge content defs that don't
// set an explicit output path, so a badge needs no per-item output: line. An explicit
// output: on a def wins. Store defaults to DefaultScribeStore.
func (s *ScribeConfig) ApplyStoreDefaults() {
	store := s.Store
	if store == "" {
		store = DefaultScribeStore
	}
	for i := range s.Content {
		c := &s.Content[i]
		if c.Output == "" && c.EffectiveKind() == "badge" {
			c.Output = path.Join(store, c.ID+".svg")
		}
	}
}

// IsZero reports whether scribe declares nothing (the phase presence gate).
func (s ScribeConfig) IsZero() bool {
	return len(s.Content) == 0 && len(s.Files) == 0 && s.Commit.IsZero()
}

// ContentByID returns a lookup map of content defs keyed by id.
func (s ScribeConfig) ContentByID() map[string]ContentDef {
	m := make(map[string]ContentDef, len(s.Content))
	for _, c := range s.Content {
		m[c.ID] = c
	}
	return m
}

// ScribeCommit is scribe's auto-commit action (was narrate.commit).
type ScribeCommit struct {
	Type    string        `yaml:"type,omitempty"`
	Message string        `yaml:"message,omitempty"`
	Add     []string      `yaml:"add,omitempty"`
	Push    bool          `yaml:"push,omitempty"`
	SkipCI  bool          `yaml:"skip_ci,omitempty"`
	RunFrom RunFromConfig `yaml:"run_from,omitempty"`
}

// IsZero reports whether the commit action is unset.
func (c ScribeCommit) IsZero() bool {
	return c.Type == "" && c.Message == "" && len(c.Add) == 0 && !c.Push && !c.SkipCI
}

// ContentDef is one renderable content definition (scribe.content[id]). Its shape
// is SOURCE (type:) × RENDER (render:): an inline def (no type) renders a badge
// (default) or shield; a named producer (type: goreportcard, github-*, contents,
// include, text, component, k8s-inventory) renders through the matching module.
// The id (map key) is the ref target and the default label.
type ContentDef struct {
	ID string `yaml:"-"` // set from the content map key

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

// EffectiveKind maps (Type, Render) to the render module used for this def. It is
// the discriminator the runtime dispatches on — the same module set the old Kind
// switch used, so render output is preserved.
func (c ContentDef) EffectiveKind() string {
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

// LabelOrID returns the badge/alt label, defaulting to the content id.
func (c ContentDef) LabelOrID() string {
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

// HasGeneration reports whether this def produces an SVG badge (an inline badge
// with an output path).
func (c ContentDef) HasGeneration() bool {
	return c.EffectiveKind() == "badge" && c.Output != ""
}

// ToBadgeSpec extracts the SVG-generation fields.
func (c ContentDef) ToBadgeSpec() BadgeSpec {
	return BadgeSpec{
		Label:    c.LabelOrID(),
		Value:    c.Message,
		Color:    c.Color,
		Output:   c.Output,
		Font:     c.Font,
		FontSize: float64(c.FontSize),
	}
}

// FileDef is one placement region (scribe.files[id]): a marked span in a file and
// the ordered content name-refs placed into it. Placement is declared once here;
// items are pure refs (plus the literal "br" for a row break).
type FileDef struct {
	ID       string    `yaml:"-"` // set from the files map key
	File     string    `yaml:"file"`
	LinkBase string    `yaml:"link_base,omitempty"`
	Between  [2]string `yaml:"between,omitempty"`
	Mode     string    `yaml:"mode,omitempty"`   // replace (default) | append | prepend | above | below
	Inline   bool      `yaml:"inline,omitempty"` // render items side-by-side
	Items    []string  `yaml:"items"`            // content ids (+ "br")
}

// validPlacementModes enumerates recognized files[].mode values.
var validPlacementModes = map[string]bool{
	"": true, "replace": true, "append": true, "prepend": true, "above": true, "below": true,
}

// OrderedContent is the content: map — id → ContentDef in document order.
type OrderedContent []ContentDef

func (o *OrderedContent) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(c *ContentDef, id string) { c.ID = id })
	if err != nil {
		return fmt.Errorf("scribe.content: %w", err)
	}
	*o = v
	return nil
}

// OrderedFiles is the files: map — id → FileDef in document order.
type OrderedFiles []FileDef

func (o *OrderedFiles) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(f *FileDef, id string) { f.ID = id })
	if err != nil {
		return fmt.Errorf("scribe.files: %w", err)
	}
	*o = v
	return nil
}
