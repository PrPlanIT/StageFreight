package discovery

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// FreshnessConfig holds per-source toggles, severity mapping, package rules,
// registry overrides, vulnerability correlation, and grouping configuration.
type FreshnessConfig struct {
	Sources       SourceConfig   `json:"sources"`
	Severity      SeverityConfig `json:"severity"`
	Vulnerability VulnConfig     `json:"vulnerability"`
	Registries    RegistryConfig `json:"registries"`
	Ignore        []string       `json:"ignore"`
	PackageRules  []PackageRule  `json:"package_rules"`
	Groups        []Group        `json:"groups"`
	Timeout       int            `json:"timeout"`   // HTTP timeout in seconds (default 10)
	CacheTTLSecs  int            `json:"cache_ttl"` // cache TTL in seconds (default 300; 0 = cache forever; <0 = never cache)

	// MinReleaseAge is a supply-chain cooldown: freshness will not recommend (and so
	// stagefreight will not auto-adopt) a release published more recently than this window.
	// Most malicious npm publishes — compromised maintainer tokens, typosquats — are caught
	// and yanked within hours to days, so a cooldown sidesteps that exposure window. Accepts
	// "7d", "2w", "72h", or any Go duration. Empty/0 = disabled. Implemented for npm (the
	// registry exposes per-version publish times); other ecosystems ignore it until wired.
	MinReleaseAge string `json:"min_release_age"`
}

// minReleaseAge parses the configured cooldown into a duration (0 = disabled).
func (c FreshnessConfig) minReleaseAge() time.Duration { return parseFlexDuration(c.MinReleaseAge) }

// parseFlexDuration extends Go duration syntax with day ("d") and week ("w") units, the
// natural granularity for a release cooldown. Returns 0 for empty/unparseable input.
func parseFlexDuration(s string) time.Duration {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	switch {
	case strings.HasSuffix(s, "d"):
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64); err == nil {
			return time.Duration(n * 24 * float64(time.Hour))
		}
	case strings.HasSuffix(s, "w"):
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "w"), 64); err == nil {
			return time.Duration(n * 7 * 24 * float64(time.Hour))
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 0
}

// VulnConfig controls vulnerability correlation via the OSV database.
//
// .stagefreight.yml example:
//
//	vulnerability:
//	  enabled: true
//	  min_severity: "moderate"
//	  severity_override: true
type VulnConfig struct {
	Enabled          *bool  `json:"enabled"`           // default true
	MinSeverity      string `json:"min_severity"`      // "low", "moderate", "high", "critical" (default: "moderate")
	SeverityOverride *bool  `json:"severity_override"` // CVE-affected deps escalate to critical (default: true)
}

// RegistryConfig overrides the default public registry URLs per ecosystem.
// Each field accepts a RegistryEndpoint; if URL is empty the public default
// is used. Auth is resolved from environment variables at runtime.
//
// .stagefreight.yml example:
//
//	registries:
//	  docker:
//	    url: "https://jcr.pcfae.com/v2"
//	    auth_env: "JCR_TOKEN"
//	  go:
//	    url: "https://goproxy.company.com"
//	  npm:
//	    url: "https://npm.company.com"
//	    auth_env: "NPM_TOKEN"
type RegistryConfig struct {
	Docker *RegistryEndpoint `json:"docker"`
	Go     *RegistryEndpoint `json:"go"`
	Npm    *RegistryEndpoint `json:"npm"`
	PyPI   *RegistryEndpoint `json:"pypi"`
	Crates *RegistryEndpoint `json:"crates"`
	Alpine *RegistryEndpoint `json:"alpine"`
	Debian *RegistryEndpoint `json:"debian"`
	Ubuntu *RegistryEndpoint `json:"ubuntu"`
	GitHub *RegistryEndpoint `json:"github"` // GitHub API for tool version checks
}

// RegistryEndpoint is a custom registry URL with optional auth.
type RegistryEndpoint struct {
	URL     string `json:"url"`      // base URL (e.g. "https://jcr.pcfae.com/v2")
	AuthEnv string `json:"auth_env"` // env var name holding auth token (Bearer)
}

// SourceConfig toggles individual dependency ecosystems on or off.
// nil means "use default" (true for all).
type SourceConfig struct {
	DockerImages   *bool `json:"docker_images"`
	GitHubReleases *bool `json:"github_releases"`
	GoModules      *bool `json:"go_modules"`
	RustCrates     *bool `json:"rust_crates"`
	NpmPackages    *bool `json:"npm_packages"`
	AlpineAPK      *bool `json:"alpine_apk"`
	DebianAPT      *bool `json:"debian_apt"`
	PipPackages    *bool `json:"pip_packages"`
}

// SeverityConfig maps version-delta levels to lint severities and
// controls how many versions behind are tolerated before reporting.
type SeverityConfig struct {
	Major int `json:"major"` // 0=info, 1=warning, 2=critical (default: 2)
	Minor int `json:"minor"` // default: 1
	Patch int `json:"patch"` // default: 0

	MajorTolerance int `json:"major_tolerance"` // default: 0
	MinorTolerance int `json:"minor_tolerance"` // default: 0
	PatchTolerance int `json:"patch_tolerance"` // default: 1
}

// PackageRule overrides severity, tolerance, or behaviour for dependencies
// that match its patterns. Rules are evaluated in order; first match wins.
// Modeled after Renovate's packageRules.
//
// .stagefreight.yml example:
//
//	package_rules:
//	  - match_packages: ["golang", "alpine"]
//	    severity: { major: 2, minor: 2, patch: 1 }
//	  - match_packages: ["*.test", "mock-*"]
//	    enabled: false
//	  - match_ecosystems: ["gomod"]
//	    match_update_types: ["patch"]
//	    automerge: true
//	    group: "go-patch-updates"
type PackageRule struct {
	// Pattern matching — all specified fields must match (AND logic).
	MatchPackages      []string `json:"match_packages"`      // glob patterns on dependency name
	MatchEcosystems    []string `json:"match_ecosystems"`    // ecosystem constants (docker-image, gomod, etc.)
	MatchUpdateTypes   []string `json:"match_update_types"`  // "major", "minor", "patch"
	MatchVulnerability *bool    `json:"match_vulnerability"` // true = only match deps with known CVEs

	// Overrides applied when matched.
	Enabled  *bool           `json:"enabled"`  // false = skip this dependency entirely
	Severity *SeverityConfig `json:"severity"` // override severity/tolerance for matched deps
	Group    string          `json:"group"`    // assign to a named group (for future MR batching)

	// Future update-mode fields (wired later, config-shape reserved now).
	Automerge *bool `json:"automerge"` // auto-merge MR if CI passes
}

// Group configures how matched dependencies are batched together for
// future MR creation. Reserved in config shape now; the MR engine
// will consume these when update mode is implemented.
//
// .stagefreight.yml example:
//
//	groups:
//	  - name: "go-patch-updates"
//	    commit_message_prefix: "deps(go): "
//	    automerge: true
//	  - name: "docker-base-images"
//	    separate_major: true
type Group struct {
	Name                string `json:"name"`
	CommitMessagePrefix string `json:"commit_message_prefix"`
	Automerge           *bool  `json:"automerge"`      // group-level automerge default
	SeparateMajor       bool   `json:"separate_major"` // split major bumps into own MR
}

// DefaultConfig returns production defaults.
func DefaultConfig() FreshnessConfig {
	return FreshnessConfig{
		Sources: SourceConfig{},
		Severity: SeverityConfig{
			Major:          2, // critical
			Minor:          1, // warning
			Patch:          0, // info
			MajorTolerance: 0,
			MinorTolerance: 0,
			PatchTolerance: 1,
		},
		Vulnerability: VulnConfig{
			MinSeverity: "moderate",
		},
		Timeout:      10,
		CacheTTLSecs: 300, // 5 minutes — aligns with registry mirror TTLs
	}
}

// CacheTTL returns the cache TTL as a time.Duration.
//   - >0 → cache with expiry (e.g. 300 → 5m)
//   - 0  → cache forever (content-hash only)
//   - <0 → never cache (always re-run)
func (c *FreshnessConfig) CacheTTL() time.Duration {
	if c.CacheTTLSecs < 0 {
		return -1 // signal "never cache" to engine
	}
	return time.Duration(c.CacheTTLSecs) * time.Second
}

// vulnEnabled returns whether vulnerability correlation is active.
func (c *FreshnessConfig) vulnEnabled() bool {
	if c.Vulnerability.Enabled == nil {
		return true // default on
	}
	return *c.Vulnerability.Enabled
}

// VulnSeverityOverride returns whether CVE-affected deps should be
// escalated to critical regardless of version delta.
func (c *FreshnessConfig) VulnSeverityOverride() bool {
	if c.Vulnerability.SeverityOverride == nil {
		return true // default on
	}
	return *c.Vulnerability.SeverityOverride
}

// parseConfig deserialises the raw YAML options map into FreshnessConfig.
// Missing fields keep their defaults.
func parseConfig(opts map[string]any) (FreshnessConfig, error) {
	cfg := DefaultConfig()
	if opts == nil {
		return cfg, nil
	}
	// Round-trip through JSON so mapstructure-style decoding works
	// without pulling in a new dependency.
	raw, err := json.Marshal(opts)
	if err != nil {
		return cfg, fmt.Errorf("freshness: marshal options: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("freshness: unmarshal options: %w", err)
	}
	return cfg, nil
}

// SourceEnabled checks whether a particular ecosystem is turned on.
func (c *FreshnessConfig) SourceEnabled(ecosystem string) bool {
	check := func(flag *bool) bool {
		if flag == nil {
			return true // default on
		}
		return *flag
	}
	switch ecosystem {
	case supplychain.EcosystemDockerImage:
		return check(c.Sources.DockerImages)
	case supplychain.EcosystemGitHubRelease:
		return check(c.Sources.GitHubReleases)
	case supplychain.EcosystemGoMod:
		return check(c.Sources.GoModules)
	case supplychain.EcosystemCargo:
		return check(c.Sources.RustCrates)
	case supplychain.EcosystemNpm:
		return check(c.Sources.NpmPackages)
	case supplychain.EcosystemAlpineAPK:
		return check(c.Sources.AlpineAPK)
	case supplychain.EcosystemDebianAPT:
		return check(c.Sources.DebianAPT)
	case supplychain.EcosystemPip:
		return check(c.Sources.PipPackages)
	default:
		return true
	}
}

// IsIgnored returns true if name matches any ignore glob or a package rule
// disables it.
func (c *FreshnessConfig) IsIgnored(name string) bool {
	for _, pattern := range c.Ignore {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// matchRule finds the first PackageRule that matches a dependency.
// Returns nil if no rule matches. All specified match fields must match (AND).
func (c *FreshnessConfig) matchRule(dep supplychain.Dependency, updateType string) *PackageRule {
	for i := range c.PackageRules {
		rule := &c.PackageRules[i]
		if !ruleMatches(rule, dep, updateType) {
			continue
		}
		return rule
	}
	return nil
}

// ruleMatches checks if all specified match fields on a rule match the dep.
// hasVuln indicates whether the dependency has known vulnerabilities.
func ruleMatches(rule *PackageRule, dep supplychain.Dependency, updateType string) bool {
	// match_vulnerability: if set, dep must have (or not have) vulns.
	if rule.MatchVulnerability != nil {
		depHasVuln := len(dep.Vulnerabilities) > 0
		if *rule.MatchVulnerability != depHasVuln {
			return false
		}
	}

	// match_packages: at least one glob must match
	if len(rule.MatchPackages) > 0 {
		matched := false
		for _, pattern := range rule.MatchPackages {
			if ok, _ := filepath.Match(pattern, dep.Name); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// match_ecosystems: dep ecosystem must be in the list
	if len(rule.MatchEcosystems) > 0 {
		matched := false
		for _, eco := range rule.MatchEcosystems {
			if eco == dep.Ecosystem {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// match_update_types: the delta type must be in the list
	if len(rule.MatchUpdateTypes) > 0 && updateType != "" {
		matched := false
		for _, ut := range rule.MatchUpdateTypes {
			if ut == updateType {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// EffectiveSeverity returns the severity config for a dependency,
// checking packageRules first, then falling back to the global config.
func (c *FreshnessConfig) EffectiveSeverity(dep supplychain.Dependency, updateType string) SeverityConfig {
	rule := c.matchRule(dep, updateType)
	if rule != nil && rule.Severity != nil {
		// Merge: rule fields override global, zero values mean "use global".
		return mergeSeverity(c.Severity, *rule.Severity)
	}
	return c.Severity
}

// IsDisabledByRule checks if a package rule explicitly disables this dep.
func (c *FreshnessConfig) IsDisabledByRule(dep supplychain.Dependency) bool {
	rule := c.matchRule(dep, "")
	if rule != nil && rule.Enabled != nil {
		return !*rule.Enabled
	}
	return false
}

// GroupForDep returns the group name assigned by a matching package rule.
func (c *FreshnessConfig) GroupForDep(dep supplychain.Dependency, updateType string) string {
	rule := c.matchRule(dep, updateType)
	if rule != nil {
		return rule.Group
	}
	return ""
}

// registryURL returns the custom URL for an ecosystem, or the provided
// default if no override is configured.
func (c *FreshnessConfig) registryURL(ecosystem, defaultURL string) string {
	ep := c.registryEndpoint(ecosystem)
	if ep != nil && ep.URL != "" {
		return ep.URL
	}
	return defaultURL
}

// registryEndpoint returns the RegistryEndpoint for an ecosystem, or nil.
func (c *FreshnessConfig) registryEndpoint(ecosystem string) *RegistryEndpoint {
	switch ecosystem {
	case supplychain.EcosystemDockerImage:
		return c.Registries.Docker
	case supplychain.EcosystemGitHubRelease:
		return c.Registries.GitHub
	case supplychain.EcosystemGoMod:
		return c.Registries.Go
	case supplychain.EcosystemNpm:
		return c.Registries.Npm
	case supplychain.EcosystemPip:
		return c.Registries.PyPI
	case supplychain.EcosystemCargo:
		return c.Registries.Crates
	case supplychain.EcosystemAlpineAPK:
		return c.Registries.Alpine
	case supplychain.EcosystemDebianAPT:
		// Check distro-specific first (handled by callers), fall back.
		return c.Registries.Debian
	default:
		return nil
	}
}

// mergeSeverity overlays rule severity onto the global defaults.
// Non-zero rule values override the global.
func mergeSeverity(global, rule SeverityConfig) SeverityConfig {
	merged := global
	if rule.Major != 0 {
		merged.Major = rule.Major
	}
	if rule.Minor != 0 {
		merged.Minor = rule.Minor
	}
	if rule.Patch != 0 {
		merged.Patch = rule.Patch
	}
	if rule.MajorTolerance != 0 {
		merged.MajorTolerance = rule.MajorTolerance
	}
	if rule.MinorTolerance != 0 {
		merged.MinorTolerance = rule.MinorTolerance
	}
	if rule.PatchTolerance != 0 {
		merged.PatchTolerance = rule.PatchTolerance
	}
	return merged
}
