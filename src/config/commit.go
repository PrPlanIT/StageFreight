package config

// Commit origins — the StageFreight path that authored a commit, and the git-native
// provenance trailer each carries. Both are our own trailers (they read like Signed-off-by,
// fire no forge CI-skip matcher, and are not forge-recognized so they trigger no avatar
// attribution). The verb is the whole signal — no parenthetical — and it lines up with the
// CI decision:
//
//	narrate → Generated-By: StageFreight   generation (docs/badges from code); no build
//	                                        impact → the CI rules SKIP its branch push.
//	deps    → Updated-By: StageFreight      an update to existing dependencies; changes what
//	                                        the image ships → it still BUILDS (rebuild + republish).
//
// A manual `stagefreight commit` or a human commit carries no trailer — SF-authorship is
// already inferable from the commit author, and it builds by default. `Co-authored-by` is
// deliberately NOT used: it's a forge-recognized attribution trailer, reserved for the day
// StageFreight authors real code, not an automated docs/deps pass.
const (
	OriginNarrate = "narrate"
	OriginDeps    = "deps"
)

const (
	// GeneratedByTrailer marks a narrate commit (generated docs/badges); the CI rules key
	// on it to skip the branch pipeline, since regenerating would only re-emit it (the loop).
	GeneratedByTrailer = "Generated-By: StageFreight"
	// UpdatedByTrailer marks a deps commit (updated dependencies); provenance only — the CI
	// does NOT skip on it, because the image must rebuild to ship the update.
	UpdatedByTrailer = "Updated-By: StageFreight"
)

// OriginTrailer returns the provenance trailer for a StageFreight commit origin, or "" for
// a manual/human commit (no trailer). The commit layer writes it; the CI-render layer keys
// its skip rule on GeneratedByTrailer.
func OriginTrailer(origin string) string {
	switch origin {
	case OriginNarrate:
		return GeneratedByTrailer
	case OriginDeps:
		return UpdatedByTrailer
	}
	return ""
}

// CommitConfig holds configuration for the commit subsystem.
type CommitConfig struct {
	Preset       string       `yaml:"preset,omitempty"`
	DefaultType  string       `yaml:"default_type,omitempty"`
	DefaultScope string       `yaml:"default_scope,omitempty"`
	SkipCI       bool         `yaml:"skip_ci,omitempty"`
	Push         bool         `yaml:"push,omitempty"`
	Conventional bool         `yaml:"conventional"`
	Backend      string       `yaml:"backend,omitempty"`
	Types        []CommitType `yaml:"types,omitempty"`
}

// CommitType defines a recognized commit type for conventional commits.
type CommitType struct {
	Key       string `yaml:"key"`
	Label     string `yaml:"label"`
	AliasFor  string `yaml:"alias_for,omitempty"`
	ForceBang bool   `yaml:"force_bang,omitempty"`
}

// DefaultCommitConfig returns sensible defaults for commit configuration.
func DefaultCommitConfig() CommitConfig {
	return CommitConfig{
		DefaultType:  "chore",
		Conventional: true,
		Backend:      "git",
		Types:        defaultCommitTypes(),
	}
}

func defaultCommitTypes() []CommitType {
	return []CommitType{
		{Key: "feat", Label: "Feature"},
		{Key: "fix", Label: "Fix"},
		{Key: "docs", Label: "Documentation"},
		{Key: "chore", Label: "Chore"},
		{Key: "refactor", Label: "Refactor"},
		{Key: "ci", Label: "CI"},
		{Key: "perf", Label: "Performance"},
		{Key: "test", Label: "Test"},
		{Key: "revert", Label: "Revert"},
		{Key: "security", Label: "Security"},
		{Key: "build", Label: "Build"},
		{Key: "breaking", Label: "Breaking", ForceBang: true},
	}
}
