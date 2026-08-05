package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AnsibleConfig defines the ansible host-convergence subsystem: a library of
// declared playbooks executed inside a containerized ansible runtime. It is
// presence-gated (any converge playbook activates the perform-phase backend)
// and deliberately independent of lifecycle.mode — ansible runs alongside
// gitops/governance, never instead of them.
type AnsibleConfig struct {
	Preset string `yaml:"preset,omitempty"`

	// Backend selects the host-convergence backend. Default: "ansible".
	Backend string `yaml:"backend"`

	// Image is the execution image the playbooks (and ansible-lint) run in —
	// the ansible runtime, collections, and connection deps are the image's
	// contract. Fully qualified, version-pinned reference.
	// Default: docker.io/hlhd/ansible (the official StageFreight ansible image).
	Image string `yaml:"image"`

	// Inventory is the repo-relative ansible inventory file.
	Inventory string `yaml:"inventory"`

	// SSH is the shared connection identity used by every play.
	SSH AnsibleSSH `yaml:"ssh"`

	// Playbooks is the play library: an order-preserving id → entry map.
	// Entries with converge: true run every perform reconcile, in declared
	// order; entries with converge: false are runbooks — lintable and
	// plannable, but only ever executed by an explicit `ansible run <id>`.
	Playbooks OrderedAnsiblePlaybooks `yaml:"playbooks"`
}

// AnsiblePlaybook is one declared play in the library. The map key is its ID.
type AnsiblePlaybook struct {
	ID string `yaml:"-"`

	// Path is the repo-relative playbook file.
	Path string `yaml:"path"`

	// Groups are the inventory groups this play targets (rendered as --limit).
	Groups []string `yaml:"groups"`

	// Converge marks the play as desired-state: it runs on every perform
	// reconcile. False declares a runbook — a named human operation that CI
	// can never trigger.
	Converge bool `yaml:"converge"`
}

// AnsibleSSH is the shared SSH connection identity for all plays.
// The private key is resolved from environment variables at runtime:
//   - <PREFIX>_SSH_KEY: PEM private key material
//   - <PREFIX>_SSH_KEY_B64: base64 of the PEM — the single-line form a forge
//     MASKED variable can hold (mirrors the gitops <NAME>_CA_B64 convention)
//   - <PREFIX>_SSH_KEY_PASSPHRASE: optional key passphrase
//
// Credentials is uppercased with hyphens replaced by underscores to form the
// env prefix (the same convention as the gitops cluster CA).
type AnsibleSSH struct {
	// User is the remote login user on the managed hosts.
	User string `yaml:"user"`

	// Credentials is the env-prefix name the SSH key material is read from.
	Credentials string `yaml:"credentials"`

	// KnownHosts is the repo-relative known_hosts file holding the managed
	// hosts' public keys. Host-key verification is always strict; the file is
	// committed so host trust is auditable in git.
	KnownHosts string `yaml:"known_hosts"`
}

// OrderedAnsiblePlaybooks is the playbooks: block — an id→playbook map
// (key becomes AnsiblePlaybook.ID), document order preserved.
type OrderedAnsiblePlaybooks []AnsiblePlaybook

func (o *OrderedAnsiblePlaybooks) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(p *AnsiblePlaybook, id string) { p.ID = id })
	if err != nil {
		return fmt.Errorf("playbooks: %w", err)
	}
	*o = v
	return nil
}

// DefaultAnsibleImage is the official execution image. Repos normally pin an
// explicit version tag; the bare default exists so local plan/lint work before
// the pin is chosen.
const DefaultAnsibleImage = "docker.io/hlhd/ansible:latest"

// DefaultAnsibleConfig returns sensible defaults for ansible configuration.
func DefaultAnsibleConfig() AnsibleConfig {
	return AnsibleConfig{
		Backend: "ansible",
		Image:   DefaultAnsibleImage,
	}
}

// HasConvergePlaybooks reports whether any play is declared converge: true —
// the presence gate for the perform-phase ansible subsystem.
func (a AnsibleConfig) HasConvergePlaybooks() bool {
	for _, p := range a.Playbooks {
		if p.Converge {
			return true
		}
	}
	return false
}

// ConvergePlaybooks returns the converge set in declared order.
func (a AnsibleConfig) ConvergePlaybooks() []AnsiblePlaybook {
	var out []AnsiblePlaybook
	for _, p := range a.Playbooks {
		if p.Converge {
			out = append(out, p)
		}
	}
	return out
}

// PlaybookByID returns the declared play with the given id.
func (a AnsibleConfig) PlaybookByID(id string) (AnsiblePlaybook, bool) {
	for _, p := range a.Playbooks {
		if p.ID == id {
			return p, true
		}
	}
	return AnsiblePlaybook{}, false
}

// EnvPrefix derives the environment prefix for SSH credential resolution:
// uppercased, hyphens to underscores (e.g. "dungeon-nodes" → "DUNGEON_NODES").
func (s AnsibleSSH) EnvPrefix() string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s.Credentials), "-", "_"))
}
