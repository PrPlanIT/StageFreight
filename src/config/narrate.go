package config

// NarrateConfig is the narrate: block — the run's stdout storytelling surface.
// announces: lists stencil ids to render as structured-output cards at the end of the
// run (stdout is a target, not an endpoint). Omitted → the shipped default, [summary].
// Any declared stencil id is announceable; the id "summary" resolves to the run's
// built-in modality story unless a user stencil shadows it (shadowing a shipped id is
// the override mechanism).
type NarrateConfig struct {
	Announces []string `yaml:"announces,omitempty"`
}

// IsZero reports whether the narrate block carries no configuration.
func (n NarrateConfig) IsZero() bool { return len(n.Announces) == 0 }
