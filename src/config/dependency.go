package config

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/paths"
)

// DependencyHandoff controls what happens when dependency repair creates a new commit.
//   - "continue"          — advisory only, current pipeline continues regardless
//   - "restart_pipeline"  — request pipeline rerun on repaired revision; downstream shipping stops
//   - "fail"              — fail hard if repair was needed but couldn't be handed off
type DependencyHandoff string

const (
	HandoffContinue        DependencyHandoff = "continue"
	HandoffRestartPipeline DependencyHandoff = "restart_pipeline"
	HandoffFail            DependencyHandoff = "fail"
)

// DependencyCIConfig controls CI-level behavior when deps creates a commit.
type DependencyCIConfig struct {
	Handoff DependencyHandoff `yaml:"handoff"` // default: continue
}

// DependencyConfig holds configuration for the dependency update subsystem.
type DependencyConfig struct {
	Preset  string                 `yaml:"preset,omitempty"`
	Enabled bool                   `yaml:"enabled"`
	Output  string                 `yaml:"output"`
	Scope   DependencyScopeConfig  `yaml:"scope"`
	Commit  DependencyCommitConfig `yaml:"commit"`
	CI      DependencyCIConfig     `yaml:"ci"`
	Ignore  []DependencyIgnore     `yaml:"ignore,omitempty"`

	// Remediate controls whether the update pass PATCHES eligible dependencies
	// (true, default — fix-forward) or only evaluates them without changing
	// anything (false). It is the module's remediation stage, orthogonal to
	// fail_on (the policy stage).
	Remediate *bool `yaml:"remediate,omitempty"`

	// FailOn is the vulnerability-severity threshold at or above which a RESIDUAL
	// vulnerability — one still present after the remediation stage (a held major,
	// no fix available, or ignored) — fails the build: "critical" | "high" |
	// "medium" | "low" | "off". Vulnerability severity vocabulary (the shared
	// severity scale), NOT lint's importance tiers. Empty defaults to "off":
	// deps stays fix-forward and never hard-fails, exactly as today.
	FailOn string `yaml:"fail_on,omitempty"`

	// Policy is the freshness SCOPE — which non-vulnerable dependencies to pursue:
	// "all" (default — every dep to its latest eligible) or "security" (only
	// vulnerable deps; leave everything else where it is). A known vulnerability is
	// a floor, remediated under EVERY policy — Policy only governs freshness of
	// NON-vulnerable deps. Empty defaults to "all" (today's audition behavior).
	Policy string `yaml:"policy,omitempty"`

	// MaxUpdate is the update-type CEILING — how far a dependency may move:
	// "major" (allow the ecosystem's own maximum), "minor" (n.x.n — lock the major,
	// allow minor+patch), or "patch" (n.n.x — lock major and minor, allow patch
	// only). Empty defaults to "major" — a NO-OP that imposes no ceiling beyond each
	// ecosystem's native compatibility model (cargo/docker still hold out-of-range
	// majors via their `^`/tag-line semantics; gomod/github-release/toolchain retain
	// their prior auto-apply). Setting "minor"/"patch" is an explicit opt-in to hold
	// majors (and minors). A security fix is applied even if it exceeds the ceiling
	// (remediation floor).
	MaxUpdate string `yaml:"max_update,omitempty"`

	// MinReleaseAge is the supply-chain COOLDOWN: a release younger than this is
	// not recommended (freshness) nor applied (deps) — dodging the window in which
	// a compromised or yanked release is briefly live. Accepts durations like
	// "3d", "72h". Owned here as a supply-chain policy; the freshness lint module
	// reads the SAME value so its recommendations match what deps will apply.
	// Empty falls back to lint.modules.freshness.options.min_release_age (its
	// historical home) for back-compat, then to disabled.
	MinReleaseAge string `yaml:"min_release_age,omitempty"`
}

// EffectivePolicy resolves the freshness scope, defaulting to "all". Lowercased
// "all" | "security".
func (c DependencyConfig) EffectivePolicy() string {
	if v := strings.ToLower(strings.TrimSpace(c.Policy)); v != "" {
		return v
	}
	return "all"
}

// EffectiveMaxUpdate resolves the update-type ceiling, defaulting to "major" — a
// no-op that preserves each ecosystem's native behavior (holding majors is an
// explicit opt-in, never a silent default). Lowercased "major" | "minor" | "patch".
func (c DependencyConfig) EffectiveMaxUpdate() string {
	if v := strings.ToLower(strings.TrimSpace(c.MaxUpdate)); v != "" {
		return v
	}
	return "major"
}

// RemediateEnabled reports whether the update pass patches eligible dependencies.
// Default: true (fix-forward).
func (c DependencyConfig) RemediateEnabled() bool {
	if c.Remediate != nil {
		return *c.Remediate
	}
	return true
}

// EffectiveFailOn resolves the residual-vulnerability gate threshold, defaulting
// to "off" (deps never hard-fails today). Lowercased "critical" | "high" |
// "medium" | "low" | "off".
func (c DependencyConfig) EffectiveFailOn() string {
	if v := strings.ToLower(strings.TrimSpace(c.FailOn)); v != "" {
		return v
	}
	return "off"
}

// DependencyIgnore suppresses a specific security advisory from remediation — an
// accepted-risk or false-positive decision. Keyed by advisory ID (the same `ignore`
// term osv-scanner/Trivy/Grype/Dependabot use), with a required reason and an expiry
// after which it lapses and the advisory re-surfaces.
type DependencyIgnore struct {
	ID     string `yaml:"id"`               // e.g. "GHSA-xxxx-yyyy-zzzz", "GO-2026-1234"
	Reason string `yaml:"reason,omitempty"` // why this risk is carried
	Until  string `yaml:"until,omitempty"`  // YYYY-MM-DD; past this date the ignore lapses
}

// DependencyScopeConfig controls which dependency ecosystems are managed.
type DependencyScopeConfig struct {
	GoModules     bool `yaml:"go_modules"`
	DockerfileEnv bool `yaml:"dockerfile_env"` // umbrella for docker-image + github-release
}

// DependencyCommitPromotion controls how dependency commits reach the target branch.
type DependencyCommitPromotion string

const (
	PromotionDirect DependencyCommitPromotion = "direct" // push to current branch (existing behavior)
	PromotionMR     DependencyCommitPromotion = "mr"     // push to unique bot branch, open merge request
)

// DependencyMRConfig controls merge request creation for promotion: mr.
type DependencyMRConfig struct {
	BranchPrefix string `yaml:"branch_prefix"` // default: "stagefreight/deps"
	TargetBranch string `yaml:"target_branch"` // default: "" (CI default branch)
}

// DependencyCommitConfig controls auto-commit behavior for dependency updates.
type DependencyCommitConfig struct {
	Enabled   bool                      `yaml:"enabled"`
	Type      string                    `yaml:"type"`
	Message   string                    `yaml:"message"`
	Push      bool                      `yaml:"push"`
	SkipCI    bool                      `yaml:"skip_ci"`
	Promotion DependencyCommitPromotion `yaml:"promotion"` // "direct" or "mr"
	MR        DependencyMRConfig        `yaml:"mr"`
	RunFrom   RunFromConfig             `yaml:"run_from,omitempty"` // gate mutation to declared origin
}

// DefaultDependencyConfig returns sensible defaults for dependency management.
func DefaultDependencyConfig() DependencyConfig {
	return DependencyConfig{
		Enabled: true,
		Output:  paths.Ephemeral("", "deps"),
		Scope: DependencyScopeConfig{
			GoModules:     true,
			DockerfileEnv: true,
		},
		Commit: DependencyCommitConfig{
			Enabled:   true,
			Type:      "chore",
			Message:   "update managed dependencies",
			Push:      true,
			SkipCI:    false,
			Promotion: PromotionDirect,
			MR: DependencyMRConfig{
				BranchPrefix: "stagefreight/deps",
			},
		},
		CI: DependencyCIConfig{
			Handoff: HandoffContinue,
		},
	}
}

// ScopeToEcosystems converts scope booleans to ecosystem filter strings
// compatible with dependency.UpdateConfig.Ecosystems.
// Returns nil (all ecosystems) if all scopes are enabled.
func (s DependencyScopeConfig) ScopeToEcosystems() []string {
	if s.GoModules && s.DockerfileEnv {
		return nil // all
	}
	var ecosystems []string
	if s.GoModules {
		ecosystems = append(ecosystems, "gomod")
	}
	if s.DockerfileEnv {
		ecosystems = append(ecosystems, "docker-image", "github-release")
	}
	return ecosystems
}
