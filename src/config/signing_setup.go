package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SigningConfig is the OPERATIONAL signing block (`signing:`), distinct from the
// trust-profile list (`signing_profiles:`). It governs whether StageFreight may
// sign at all, whether it may create/manage Tier-0 identity material on the
// operator's behalf, and where that material persists.
//
// The two toggles are deliberately separate — collapsing them would let the system
// silently mint a trust identity nobody consented to:
//   - enabled        = "StageFreight may perform signing"
//   - auto_provision = "StageFreight may create/manage a Tier-0 identity"
//
// `auto_provision` defaults FALSE: with no config, StageFreight mints nothing and
// signs only with an explicit key/profile. Opinionated "always-on" defaults belong
// in a runner/distribution config (visible, editable, removable), never in core.
type SigningConfig struct {
	Enabled       *bool    `yaml:"enabled,omitempty"`        // nil/true = signing allowed; false = all signing off
	AutoProvision bool     `yaml:"auto_provision,omitempty"` // explicit consent to create/manage a Tier-0 identity
	StateDir      StateDir `yaml:"state_dir,omitempty"`      // where persistent signing material lives
}

// StateDir locates the durable, operator-chosen home for persistent signing
// material — outside the build tree so it is never cleared, committed, or baked.
type StateDir struct {
	Type string `yaml:"type,omitempty"` // "volume" | "host_path"
	Name string `yaml:"name,omitempty"` // volume name (type: volume)
	Path string `yaml:"path,omitempty"` // absolute path (type: host_path)
}

// stateVolumeBase is the conventional in-container mount base a runner mounts a
// named signing volume under. The runner is responsible for the actual mount; this
// is the agreed path StageFreight reads.
const stateVolumeBase = "/var/lib/stagefreight"

// SigningEnabled reports whether signing may run. Default (unset) is true —
// signing is a first-class capability; `enabled: false` is the global kill switch.
func (c SigningConfig) SigningEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Configured reports whether a state dir is declared at all.
func (s StateDir) Configured() bool {
	return strings.TrimSpace(s.Type) != ""
}

// Resolve maps the declared state dir to a filesystem path. Empty type → "" (not
// configured → no Tier-0 auto-provision).
func (s StateDir) Resolve() (string, error) {
	switch s.Type {
	case "":
		return "", nil
	case "host_path":
		if strings.TrimSpace(s.Path) == "" {
			return "", fmt.Errorf("signing.state_dir.path is required for type host_path")
		}
		return s.Path, nil
	case "volume":
		name := strings.TrimSpace(s.Name)
		if name == "" {
			name = "stagefreight-signing"
		}
		return filepath.Join(stateVolumeBase, name), nil
	default:
		return "", fmt.Errorf("signing.state_dir.type %q invalid (expected volume or host_path)", s.Type)
	}
}

// ValidateSigningConfig checks the operational signing block. auto_provision needs
// a state dir (nowhere to persist a Tier-0 identity otherwise — a silent ephemeral
// key would break continuity on every run).
func ValidateSigningConfig(c SigningConfig) []string {
	var errs []string
	if _, err := c.StateDir.Resolve(); err != nil {
		errs = append(errs, "signing: "+err.Error())
	}
	if c.AutoProvision && !c.StateDir.Configured() {
		errs = append(errs, "signing.auto_provision is true but signing.state_dir is not configured — there is nowhere to persist a Tier-0 identity (an ephemeral key would break trust continuity every run)")
	}
	return errs
}
