package config

// PresentationConfig defines surface-specific rendering policies.
// Same semantic meaning, different editorial treatment per output.
type PresentationConfig struct {
	Preset  string              `yaml:"preset,omitempty"`
	Commit  CommitPresentation  `yaml:"commit"`
	Tag     TagPresentation     `yaml:"tag"`
	Release ReleasePresentation `yaml:"release"`
}

// CommitPresentation controls commit authoring behavior.
// Authoring-oriented: alias expansion and validation, NOT release-style uplift.
type CommitPresentation struct {
	PreserveRawSubject  bool `yaml:"preserve_raw_subject"`
	EnforceConventional bool `yaml:"enforce_conventional"`
}

// TagPresentation controls tag annotation rendering.
type TagPresentation struct {
	MaxEntries             int    `yaml:"max_entries"`
	GroupByType            bool   `yaml:"group_by_type"`
	Style                  string `yaml:"style"` // concise | explanatory | technical
	IncludeReleaseVisibleOnly bool `yaml:"include_release_visible_only"`
	CollapseSimilar        bool   `yaml:"collapse_similar"`
}

// ReleasePresentation controls release notes rendering.
type ReleasePresentation struct {
	MaxEntries             int    `yaml:"max_entries"`
	GroupByType            bool   `yaml:"group_by_type"`
	Style                  string `yaml:"style"` // concise | explanatory | technical
	IncludeReleaseVisibleOnly bool `yaml:"include_release_visible_only"`
}

// DefaultPresentationConfig returns sensible defaults.
func DefaultPresentationConfig() PresentationConfig {
	return PresentationConfig{
		Commit: CommitPresentation{
			PreserveRawSubject:  true,
			EnforceConventional: true,
		},
		Tag: TagPresentation{
			MaxEntries:                8,
			GroupByType:               false,
			Style:                     "concise",
			IncludeReleaseVisibleOnly: true,
			CollapseSimilar:           true,
		},
		Release: ReleasePresentation{
			MaxEntries:                20,
			GroupByType:               true,
			Style:                     "explanatory",
			IncludeReleaseVisibleOnly: true,
		},
	}
}
