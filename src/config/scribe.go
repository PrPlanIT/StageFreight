package config

import (
	"fmt"
	"path"

	"github.com/PrPlanIT/StageFreight/src/paths"
	"gopkg.in/yaml.v3"
)

// DefaultScribeStore is the default directory for scribe-rendered file assets (badge
// SVGs, and any future rendered files). Named after the subsystem, consistent with
// .stagefreight/security, .stagefreight/deps, .stagefreight/manifests. Derived from
// paths so the durable allowlist (workspace) carves out this exact directory.
var DefaultScribeStore = path.Join(paths.Root, paths.ScribeName)

// ScribeConfig is the scribe: block — place rendered stencils into repository files
// and commit them: files: (placement regions whose bodies reference stencils by
// {id}) + commit:. The reusable stencil library it draws from lives at the top-level
// stencils: block (Config.Stencils) — shared with narrate and release.
type ScribeConfig struct {
	Store  string       `yaml:"store,omitempty"`  // dir for rendered file assets (default .stagefreight/scribe); path = {store}/{id}.svg
	Files  OrderedFiles `yaml:"files,omitempty"`  // id → placement region referencing stencils
	Commit ScribeCommit `yaml:"commit,omitempty"` // scribe's own auto-commit action
}

// IsZero reports whether scribe declares nothing (the phase presence gate). Note it
// does NOT consider stencils: the library is presence-neutral (shared, may exist with
// no scribe placement), so only files/commit gate the scribe/narrate stage.
func (s ScribeConfig) IsZero() bool {
	return len(s.Files) == 0 && s.Commit.IsZero()
}

// ScribeCommit is scribe's auto-commit action.
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

// FileDef is one placement region (scribe.files[id]): a marked span in a file and the
// markdown placed into it. body: is freeform markdown with {id} stencil embeds (+ {#if});
// items: is sugar for a body (a row of stencils, "br" for a row break, inline: for
// side-by-side). One or the other — body: wins when both are set.
type FileDef struct {
	ID       string    `yaml:"-"` // set from the files map key
	File     string    `yaml:"file"`
	LinkBase string    `yaml:"link_base,omitempty"`
	Between  [2]string `yaml:"between,omitempty"`
	Inline   bool      `yaml:"inline,omitempty"` // items sugar: render side-by-side
	Items    []string  `yaml:"items,omitempty"`  // stencil ids (+ "br"); sugar for a body
	Body     string    `yaml:"body,omitempty"`   // freeform markdown with {id} embeds
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
