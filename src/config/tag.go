package config

// TagConfig holds workflow defaults for the tag planner.
type TagConfig struct {
	Preset   string      `yaml:"preset,omitempty"`
	Defaults TagDefaults `yaml:"defaults"`
	Message  TagMessage  `yaml:"message"`
}

// TagDefaults controls default tag planning behavior.
type TagDefaults struct {
	Target          string `yaml:"target"`           // default ref to tag (default: HEAD)
	Preview         bool   `yaml:"preview"`          // show preview before creating
	RequireApproval bool   `yaml:"require_approval"` // require interactive approval
	Push            bool   `yaml:"push"`             // push after creation
}

// TagMessage controls tag annotation message behavior.
type TagMessage struct {
	Mode          string `yaml:"mode"`           // auto | prompt_if_missing | require_manual
	EmptyStrategy string `yaml:"empty_strategy"` // prompt | fail | allow_empty
}

// DefaultTagConfig returns sensible defaults.
func DefaultTagConfig() TagConfig {
	return TagConfig{
		Defaults: TagDefaults{
			Target:          "HEAD",
			Preview:         true,
			RequireApproval: true,
			Push:            false,
		},
		Message: TagMessage{
			Mode:          "prompt_if_missing",
			EmptyStrategy: "prompt",
		},
	}
}
