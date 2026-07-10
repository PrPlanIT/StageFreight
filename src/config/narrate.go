package config

// NarrateConfig configures the Narrate phase — the deterministic, toolchain-free work
// that runs after Perform/Review/Publish: render badges from build metadata, apply
// marked-region patches to files, and commit the results. Presence-enabled: a
// sub-section runs because it is configured, not via a toggle. (Reference docs are NOT
// here — they are a `kind: command` build; Narrate only commits their output.)
type NarrateConfig struct {
	// Badges are SVG badge definitions rendered from build metadata (was top-level
	// `badges.items`). Narrate patches reference them by id via `kind: badge_ref`.
	Badges []BadgeConfig `yaml:"badges,omitempty"`

	// Patches are generic marked-region replacements in files (was `narrator:`): each
	// entry names a file and the items placed between its sf-markers. Works on any file.
	Patches []NarratorFile `yaml:"patches,omitempty"`

	// Commit is the auto-commit action for generated output (was `docs.commit`).
	Commit NarrateCommitConfig `yaml:"commit,omitempty"`
}

// NarrateCommitConfig is the auto-commit ACTION — it uses the top-level `commit:` engine
// for formatting/backend and adds: which command-builds' trees to land in the repo
// (Builds) and which paths to stage (Add).
type NarrateCommitConfig struct {
	Type    string                `yaml:"type,omitempty"`    // conventional type; default: engine's
	Message string                `yaml:"message,omitempty"`
	Add     []string              `yaml:"add,omitempty"`
	Push    bool                  `yaml:"push,omitempty"`
	SkipCI  bool                  `yaml:"skip_ci,omitempty"`
	RunFrom RunFromConfig         `yaml:"run_from,omitempty"` // gate mutation to declared origin
	Builds  []NarrateBuildBinding `yaml:"builds,omitempty"`   // extract build tree → destination, then stage
}

// NarrateBuildBinding lands a command-build's output tree at a repo destination before
// the commit stages it — the producer (build) owns what it made; this owns where it goes.
type NarrateBuildBinding struct {
	Build       string `yaml:"build"`
	Destination string `yaml:"destination"`
}

// IsZero reports whether nothing is configured (Narrate is inactive).
func (n NarrateConfig) IsZero() bool {
	return len(n.Badges) == 0 && len(n.Patches) == 0 && n.Commit.IsZero()
}

// IsZero reports whether the commit action has nothing to do.
func (c NarrateCommitConfig) IsZero() bool {
	return c.Type == "" && c.Message == "" && len(c.Add) == 0 && len(c.Builds) == 0 &&
		!c.Push && !c.SkipCI
}
