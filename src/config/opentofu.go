package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// OpenTofuConfig defines the OpenTofu/Terraform infrastructure-convergence subsystem:
// a library of declared root modules planned and applied by the backend's binary. It is
// presence-gated (any converge root activates the perform-phase backend) and
// deliberately independent of lifecycle.mode — it runs alongside ansible and
// gitops/governance, never instead of them.
type OpenTofuConfig struct {
	Preset string `yaml:"preset,omitempty"`

	// Backend selects the binary that runs the roots: "opentofu" or "terraform".
	// Default: "opentofu". Its version resolves from toolchains.want.<backend>.
	Backend string `yaml:"backend"`

	// Credentials, when set, is an env prefix: every <PREFIX>_<NAME> variable reaches the
	// binary as <NAME>, and CI skips the converge when none is set. Unset, the binary reads
	// the environment as it is.
	Credentials string `yaml:"credentials,omitempty"`

	// Roots is the root-module library: an order-preserving id → entry map. Entries
	// with converge: true are planned and applied every perform reconcile, in declared
	// order; entries with converge: false are runbooks — linted and plannable, but only
	// ever applied by an explicit `opentofu run <id>`.
	Roots OrderedOpenTofuRoots `yaml:"roots"`
}

// OpenTofuRoot is one declared root module. The map key is its ID.
type OpenTofuRoot struct {
	ID string `yaml:"-"`

	// Path is the repo-relative root-module directory.
	Path string `yaml:"path"`

	// Converge marks the root as desired state: planned and applied on every perform
	// reconcile. False declares a runbook — a named human operation that CI can never
	// trigger.
	Converge bool `yaml:"converge"`

	// Required: failure is hard pipeline fail (default: true — an apply mutates
	// infrastructure, so a partial apply must never pass silently). Set false for
	// advisory roots: a failure is recorded and narrated but the phase still exits clean.
	Required *bool `yaml:"required,omitempty"`

	// Workspace is the workspace the root runs in (selected, created when absent).
	// Default: the binary's "default" workspace.
	Workspace string `yaml:"workspace,omitempty"`

	// VarFiles are -var-file arguments, relative to Path, in declared order.
	VarFiles []string `yaml:"var_files,omitempty"`

	// OnDestroy is what a converge does with a plan that destroys or replaces anything:
	// refuse (default) fails the root without applying; apply applies it. opentofu run
	// applies it either way.
	OnDestroy string `yaml:"on_destroy,omitempty"`
}

// On-destroy outcomes.
const (
	OpenTofuOnDestroyRefuse = "refuse"
	OpenTofuOnDestroyApply  = "apply"
)

// AppliesDestroys reports whether an unattended converge applies a destroying plan.
func (r OpenTofuRoot) AppliesDestroys() bool { return r.OnDestroy == OpenTofuOnDestroyApply }

// IsRequired reports whether this root's failure hard-fails the phase. Unset defaults to true.
func (r OpenTofuRoot) IsRequired() bool {
	return r.Required == nil || *r.Required
}

// OrderedOpenTofuRoots is the roots: block — an id→root map (key becomes
// OpenTofuRoot.ID), document order preserved.
type OrderedOpenTofuRoots []OpenTofuRoot

func (o *OrderedOpenTofuRoots) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(r *OpenTofuRoot, id string) { r.ID = id })
	if err != nil {
		return fmt.Errorf("roots: %w", err)
	}
	*o = v
	return nil
}

// OpenTofu backends.
const (
	OpenTofuBackendOpenTofu  = "opentofu"
	OpenTofuBackendTerraform = "terraform"
)

// DefaultOpenTofuConfig returns sensible defaults for opentofu configuration.
func DefaultOpenTofuConfig() OpenTofuConfig {
	return OpenTofuConfig{Backend: OpenTofuBackendOpenTofu}
}

// HasConvergeRoots reports whether any root is declared converge: true — the presence
// gate for the perform-phase opentofu subsystem.
func (o OpenTofuConfig) HasConvergeRoots() bool {
	for _, r := range o.Roots {
		if r.Converge {
			return true
		}
	}
	return false
}

// ConvergeRoots returns the converge set in declared order.
func (o OpenTofuConfig) ConvergeRoots() []OpenTofuRoot {
	var out []OpenTofuRoot
	for _, r := range o.Roots {
		if r.Converge {
			out = append(out, r)
		}
	}
	return out
}

// RootByID returns the declared root with the given id.
func (o OpenTofuConfig) RootByID(id string) (OpenTofuRoot, bool) {
	for _, r := range o.Roots {
		if r.ID == id {
			return r, true
		}
	}
	return OpenTofuRoot{}, false
}

// EnvPrefix derives the environment prefix for credential resolution: uppercased,
// hyphens to underscores (e.g. "oracle-site" → "ORACLE_SITE").
func (o OpenTofuConfig) EnvPrefix() string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(o.Credentials), "-", "_"))
}

// CredentialEnv returns the <PREFIX>_<NAME> variables from environ as NAME=value
// pairs, the prefix stripped. Empty when no credentials are declared or none are set.
func (o OpenTofuConfig) CredentialEnv(environ []string) []string {
	prefix := o.EnvPrefix()
	if prefix == "" {
		return nil
	}
	prefix += "_"
	var out []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		rest := kv[len(prefix):]
		if eq := strings.IndexByte(rest, '='); eq > 0 && rest[eq+1:] != "" {
			out = append(out, rest)
		}
	}
	return out
}

// CredentialsPresent reports whether the declared credentials are available: always with
// none declared, else when any <PREFIX>_<NAME> is set. Credentials ship as forge-protected
// variables, so MR/unprotected-ref pipelines legitimately lack them.
func (o OpenTofuConfig) CredentialsPresent() bool {
	return strings.TrimSpace(o.Credentials) == "" || len(o.CredentialEnv(os.Environ())) > 0
}
