package config

// Render policies live WITH the block they render: commit.render, tagging.render,
// release.render (there is no top-level presentation: — it was dissolved).

// CommitPresentation controls commit rendering (commit.render). Authoring-oriented:
// alias expansion and validation, NOT release-style uplift.
type CommitPresentation struct {
	PreserveRawSubject  bool `yaml:"preserve_raw_subject"`
	EnforceConventional bool `yaml:"enforce_conventional"`
}

// TagPresentation controls tag annotation rendering (tagging.render).
type TagPresentation struct {
	MaxEntries                int    `yaml:"max_entries"`
	GroupByType               bool   `yaml:"group_by_type"`
	Style                     string `yaml:"style"` // concise | explanatory | technical
	IncludeReleaseVisibleOnly bool   `yaml:"include_release_visible_only"`
	CollapseSimilar           bool   `yaml:"collapse_similar"`
}

// ReleasePresentation controls release notes rendering (release.render).
type ReleasePresentation struct {
	MaxEntries                int    `yaml:"max_entries"`
	GroupByType               bool   `yaml:"group_by_type"`
	Style                     string `yaml:"style"` // concise | explanatory | technical
	IncludeReleaseVisibleOnly bool   `yaml:"include_release_visible_only"`
}

// DefaultCommitPresentation is the default commit.render policy.
func DefaultCommitPresentation() CommitPresentation {
	return CommitPresentation{PreserveRawSubject: true, EnforceConventional: true}
}

// DefaultTagPresentation is the default tagging.render policy.
func DefaultTagPresentation() TagPresentation {
	return TagPresentation{
		MaxEntries:                8,
		Style:                     "concise",
		IncludeReleaseVisibleOnly: true,
		CollapseSimilar:           true,
	}
}

// DefaultReleasePresentation is the default release.render policy.
func DefaultReleasePresentation() ReleasePresentation {
	return ReleasePresentation{
		MaxEntries:                20,
		GroupByType:               true,
		Style:                     "explanatory",
		IncludeReleaseVisibleOnly: true,
	}
}
