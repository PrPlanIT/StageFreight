package config

import "strings"

// Lifecycle mode identifiers — the CANONICAL set. A mode selects phase-BODY
// behavior (build vs reconcile) and which mode-specific config block applies; it
// never gates a phase — the five phases (audition/perform/review/publish/narrate)
// run for every mode. These constants and the lifecycleModes table below are the
// single source of truth: dispatch sites and the validator read them instead of
// hardcoding `case "gitops"` literals.
const (
	ModeImage      = "image"
	ModeGitops     = "gitops"
	ModeDocker     = "docker"
	ModeGovernance = "governance"
)

// LifecycleMode is the fact-set for one mode.
type LifecycleMode struct {
	// Name is the canonical (lowercased) mode identifier.
	Name string

	// PhaseReconcile reports whether the perform/audition/review/publish phase
	// BODIES reconcile for this mode instead of build/review/publish. True for
	// gitops+governance. False for image (build) and docker — docker reconciles
	// via the `docker`/`reconcile` CLI (RunLifecycle), not the CI phase bodies.
	PhaseReconcile bool

	// Backend returns the RunLifecycle backend name for this mode, or nil when the
	// mode has no RunLifecycle backend: image (builds via the CI pipeline) and
	// governance (dispatched by its own subsystem, executeGovernanceReconcile —
	// it never reaches RunLifecycle).
	Backend func(*Config) string
}

var lifecycleModes = map[string]LifecycleMode{
	ModeImage:      {Name: ModeImage, PhaseReconcile: false},
	ModeGitops:     {Name: ModeGitops, PhaseReconcile: true, Backend: func(c *Config) string { p, _ := c.GitOps.Primary(); return p.Backend }},
	ModeDocker:     {Name: ModeDocker, PhaseReconcile: false, Backend: func(c *Config) string { return c.Docker.Backend }},
	ModeGovernance: {Name: ModeGovernance, PhaseReconcile: true},
}

// LookupMode canonicalizes a raw lifecycle.mode string (lowercased, trimmed) and
// resolves its fact-set. Empty ≡ image (the live default the phase bodies apply
// via their `default:` arm). ok=false for an unknown mode — the validator uses
// this to reject typos.
func LookupMode(raw string) (LifecycleMode, bool) {
	m := strings.ToLower(strings.TrimSpace(raw))
	if m == "" {
		m = ModeImage
	}
	lm, ok := lifecycleModes[m]
	return lm, ok
}

// Mode resolves this config's lifecycle mode for DISPATCH. An unknown mode falls
// back to the image fact-set — the same outcome the phase bodies' `default:` arm
// gives — because validation rejects unknown modes at load, so a dispatch-time
// lookup can safely treat anything unresolved as the build default.
func (c *Config) Mode() LifecycleMode {
	if lm, ok := LookupMode(c.Lifecycle.Mode); ok {
		return lm
	}
	return lifecycleModes[ModeImage]
}
