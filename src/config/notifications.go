package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Notification is one entry in the top-level notifications: block — endpoint and
// message FUSED in each entry (deliberately no notifier library: the library
// pattern pays when actions fan many-to-few, and a notification is one-to-one —
// message and destination are the same thought; shared topic URLs are what vars:
// is for). subject/body/click are freeform stencil bodies (facts + {stencil}
// embeds); omitted, they default to the shipped subject and the run's arc body.
// Gating uses the ONE when: grammar (events/branches/git_tags) extended with the
// outcomes: dimension; omitted when: = always.
type Notification struct {
	ID       string `yaml:"-"`        // the map key
	Provider string `yaml:"provider"` // ntfy | webhook

	// Transport. Credentials follows the shipped env-prefix convention
	// (credentials: NTFY → NTFY_TOKEN, sent as Authorization: Bearer).
	URL         string `yaml:"url,omitempty"`
	Credentials string `yaml:"credentials,omitempty"`

	// Message — freeform stencil bodies.
	Subject string `yaml:"subject,omitempty"`
	Body    string `yaml:"body,omitempty"`

	// When gates dispatch: outcomes: (success | failure | warning) composing with
	// branches:/events:/git_tags: ("failures, but only on main"). Omitted = always.
	When WhenConditions `yaml:"when,omitempty"`

	// MaxLength hard-caps the rendered body in bytes (ntfy's default server limit
	// is 4096). Trimming happens at a line boundary and always preserves the
	// pipeline-link line so tap-through survives. 0 = no cap.
	MaxLength int `yaml:"max_length,omitempty"`

	// ntfy knobs — the full header vocabulary.
	Priority string       `yaml:"priority,omitempty"` // min|low|default|high|max
	Tags     StringOrList `yaml:"tags,omitempty"`     // emoji tags (comma-joined into the Tags header)
	Click    string       `yaml:"click,omitempty"`    // tap-through URL (stencil body; default {pipeline_url})
	Attach   string       `yaml:"attach,omitempty"`   // attachment URL
	Actions  string       `yaml:"actions,omitempty"`  // ntfy actions spec string
	Markdown bool         `yaml:"markdown,omitempty"` // render body as markdown
	Email    string       `yaml:"email,omitempty"`    // forward to email address
}

// OrderedNotifications is the notifications: map — id → Notification in document order.
type OrderedNotifications []Notification

func (o *OrderedNotifications) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(t *Notification, id string) { t.ID = id })
	if err != nil {
		return fmt.Errorf("notifications: %w", err)
	}
	*o = v
	return nil
}

// IsZero reports whether no notifications are declared (the dispatch gate).
func (o OrderedNotifications) IsZero() bool { return len(o) == 0 }
