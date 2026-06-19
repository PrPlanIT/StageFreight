package freshness

// Dependency is a version-pinned reference extracted from a project file.
// It is the bridge type consumed by both lint reporting and future update
// commands (à la Renovate managers).
type Dependency struct {
	Name    string // e.g. "golang", "github.com/spf13/cobra", "react"
	Current string // currently pinned version string

	// Latest is the newest version the registry publishes — "latest AVAILABLE".
	// It is NOT necessarily a safe autonomous-update target: a major / out-of-range
	// bump can break the build (feature renames, API migrations). See LatestEligible.
	Latest string

	// LatestEligible is the newest version that is semver-COMPATIBLE with the current
	// constraint — the safe autonomous-remediation target. Empty for ecosystems with
	// no compatibility model (exact go.mod pins), where Latest is the target. When
	// Latest > LatestEligible a major upgrade exists OUT of range: review-domain,
	// constraint-expanding, never auto-applied.
	LatestEligible string

	Ecosystem string // one of the Ecosystem* constants below
	File      string // relative path from repo root
	Line      int    // line number of the pinned version
	Indirect  bool   // e.g. go.mod // indirect
	SourceURL string // registry/API URL that was queried
	Binding   string // editable anchor used by source-specific updaters (e.g. ENV var name)

	// Vulnerability info populated by the OSV correlation pass.
	Vulnerabilities []VulnInfo // known CVEs affecting the current version

	// Advisory is an optional informational message set by the resolver
	// when a non-versioned or pre-release tag has stable releases available.
	Advisory string

	// ResolutionError records why Latest could NOT be determined — a registry
	// lookup failure, an empty response, a parse error. When set, the dependency
	// is UNRESOLVED: an indeterminate state that must never be rendered as
	// up-to-date. StageFreight never claims freshness it failed to verify.
	ResolutionError string

	// Fields populated by the config/rule engine after resolution.
	// Used by future update commands for MR grouping and automerge.
	Group     string // assigned group name from package rules
	Automerge bool   // whether this dep's MR should automerge
}

// UpdateTarget is the version autonomous remediation should advance to: the latest
// semver-COMPATIBLE version when known, else the latest available (ecosystems with no
// compatibility model, e.g. exact go.mod pins). This is the perform-domain action.
func (d Dependency) UpdateTarget() string {
	if d.LatestEligible != "" {
		return d.LatestEligible
	}
	return d.Latest
}

// MajorAvailable reports whether a newer version exists OUTSIDE the compatible range —
// a constraint-expanding upgrade. That is a review-domain change (may need feature
// renames / API migration), never autonomous.
func (d Dependency) MajorAvailable() bool {
	return d.LatestEligible != "" && d.Latest != "" && d.Latest != d.LatestEligible
}

// VulnInfo describes a single known vulnerability affecting a dependency.
type VulnInfo struct {
	ID       string // e.g. "GHSA-xxxx-yyyy-zzzz", "CVE-2024-12345"
	Summary  string // short description
	Severity string // "LOW", "MODERATE", "HIGH", "CRITICAL" (from OSV/CVSS)
	FixedIn  string // version that fixes the vulnerability (if known)
	Source   string // provenance: "osv" (default), "trivy", "grype", "trivy+grype"
}

// Ecosystem constants identify the origin of a dependency.
const (
	EcosystemDockerImage   = "docker-image"
	EcosystemGitHubRelease = "github-release"
	EcosystemGoMod         = "gomod"
	EcosystemToolchain     = "toolchain"
	EcosystemCargo         = "cargo"
	EcosystemNpm           = "npm"
	EcosystemAlpineAPK     = "alpine-apk"
	EcosystemDebianAPT     = "debian-apt"
	EcosystemPip           = "pip"
)
