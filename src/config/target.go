package config

// TargetConfig defines a distribution target or side-effect. Each target has a
// unique ID and a kind that determines which fields are valid.
//
// This is a discriminated union keyed by Kind. Only fields relevant to the kind
// should be set — validated at load time.
//
// Target kinds:
//   - registry: Push image tags to a container registry (requires build reference)
//   - docker-readme: Sync README to a container registry (standalone)
//   - gitlab-component: Publish to GitLab CI component catalog (standalone)
//   - release: Create forge release + rolling git tags (standalone)
//   - generic-package: Publish archives to a forge generic package registry (standalone)
//   - pages: Deploy a static-site build's output to Cloudflare/GitHub Pages (build XOR dir)
type TargetConfig struct {
	// ID is the unique identifier for this target (logging, status, enable/disable).
	ID string `yaml:"id"`

	// Kind is the target type. Determines which fields are valid.
	Kind string `yaml:"kind"`

	// Build references a BuildConfig.ID. Required for kind: registry.
	Build string `yaml:"build,omitempty"`

	// When specifies routing conditions for this target.
	When TargetCondition `yaml:"when,omitempty"`

	// RunFrom gates mutation to declared execution origins.
	// e.g. ["primary"] — only mutate when running from sources.primary.
	// Empty = no restriction (backward compat).
	RunFrom RunFromConfig `yaml:"run_from,omitempty"`

	// SelectTags enables CLI filtering via --select.
	SelectTags []string `yaml:"select_tags,omitempty"`

	// Registry references registries[].id for registry/docker-readme targets. Accepts
	// a single id (registry: harbor) or a list (registry: [dockerhub, ghcr, harbor]) —
	// a list fans the target across registries: expandMultiRegistryTargets splits it
	// into one single-registry target per id at load, so resolution/execution always
	// sees exactly one registry. When set, URL/Provider/Path/Credentials resolve from
	// the registry entry. Path can still be overridden on the target.
	Registry StringOrList `yaml:"registry,omitempty"`

	// SigningProfile references a signing_profiles[].id — the trust profile this
	// target signs under. Empty = the synthesized `legacy` profile (key-signing,
	// inert unless a key resolves). Reference-by-id, same pattern as Registry.
	SigningProfile string `yaml:"signing_profile,omitempty"`

	// ── Shared fields (used by multiple kinds, legacy when registry: is set) ──

	// URL is the registry/forge hostname (kind: registry, docker-readme, release).
	URL string `yaml:"url,omitempty"`

	// Provider is the vendor type for auth and API behavior.
	// Registry: docker, ghcr, gitlab, jfrog, harbor, quay, gitea, generic.
	// Release: github, gitlab, gitea.
	// If omitted on registry/docker-readme, inferred from URL.
	Provider string `yaml:"provider,omitempty"`

	// Path is the image path within the registry (kind: registry, docker-readme).
	Path string `yaml:"path,omitempty"`

	// Credentials is the env var prefix for authentication.
	// Resolution: try {PREFIX}_TOKEN first, else {PREFIX}_USER + {PREFIX}_PASS.
	Credentials string `yaml:"credentials,omitempty"`

	// Description is a short description override (kind: registry, docker-readme).
	Description string `yaml:"description,omitempty"`

	// Retention controls cleanup of old tags/releases.
	// Structured only in v2 (no scalar shorthand).
	Retention *RetentionPolicy `yaml:"retention,omitempty"`

	// ── kind: registry ────────────────────────────────────────────────────

	// Tags are tag templates resolved against version info (kind: registry).
	// e.g., ["{version}", "{major}.{minor}", "latest"]
	Tags []string `yaml:"tags,omitempty"`

	// NativeScan enables post-push vulnerability scanning via the registry's own built-in scanner.
	// Distinct from StageFreight's own scan pipeline (Trivy/Grype run by StageFreight itself).
	// Currently supported: Harbor (triggers Harbor's built-in Trivy after each push).
	// No-op for Docker Hub, GHCR, Quay, JFrog, and other providers.
	// Best-effort — scan failures warn but do not fail the build.
	// Push success does not imply scan success; results appear in the registry UI only.
	NativeScan bool `yaml:"native_scan,omitempty"`

	// ── kind: docker-readme ───────────────────────────────────────────────

	// File is the path to the README file (kind: docker-readme).
	File string `yaml:"file,omitempty"`

	// LinkBase is the base URL for relative link rewriting (kind: docker-readme).
	LinkBase string `yaml:"link_base,omitempty"`

	// ── kind: gitlab-component ────────────────────────────────────────────

	// SpecFiles lists component spec file paths (kind: gitlab-component).
	SpecFiles []string `yaml:"spec_files,omitempty"`

	// Catalog enables GitLab Catalog registration (kind: gitlab-component).
	Catalog bool `yaml:"catalog,omitempty"`

	// ── kind: release ─────────────────────────────────────────────────────

	// Aliases are rolling git tag aliases (kind: release).
	// e.g., ["{version}", "{major}.{minor}", "latest"]
	// Named "aliases" to avoid collision with Tags (image tags) and git_tags (policy filters).
	Aliases []string `yaml:"aliases,omitempty"`

	// Tag is the immutable identity pattern for a release channel (kind: release).
	// Distinct from Aliases (rolling): Tag names one immutable release per build, e.g.
	// "dev-{sha:8}". Resolved against version info like Aliases ({version}, {sha:8}, ...).
	// When the triggering event is not itself a ref (a branch push), the release runner
	// mints this tag so the channel release has a stable anchor. Empty = no channel tag.
	Tag string `yaml:"tag,omitempty"`

	// Type is the semantic release classification (kind: release):
	//
	//	type: latest        — the stable, "Latest" release (GitHub: make_latest=true)
	//	type: prerelease    — a pre-release (never Latest)
	//	type: unspecified   — explicit default/auto (declarative GitOps; same as omitting)
	//
	// Omitting the field is identical to "unspecified": the type is inferred from the
	// version (a semver prerelease suffix ⇒ prerelease) and the legacy `prerelease` flag.
	// Each forge lowers the intent to what it can express (GitHub: prerelease+make_latest;
	// Gitea: prerelease; GitLab: a plain release; Azure: n/a).
	Type string `yaml:"type,omitempty"`

	// Prerelease marks the forge release as a pre-release (kind: release). DEPRECATED:
	// prefer `type: prerelease`. Still honored when `type` is unset/unspecified.
	Prerelease bool `yaml:"prerelease,omitempty"`

	// ProjectID is the "owner/repo" or numeric ID for remote forge targets (kind: release).
	ProjectID string `yaml:"project_id,omitempty"`

	// Mirror references a sources.mirrors[].id for release sync.
	// Forge identity (provider, url, project_id, credentials) is resolved from the mirror.
	// Avoids restating forge connection details in the target.
	Mirror string `yaml:"mirror,omitempty"`

	// SyncRelease syncs release notes + tags to a remote forge (kind: release, remote only).
	SyncRelease bool `yaml:"sync_release,omitempty"`

	// SyncAssets syncs scan artifacts to a remote forge (kind: release, remote only).
	SyncAssets bool `yaml:"sync_assets,omitempty"`

	// ── kind: binary-archive ──────────────────────────────────────────────

	// Archives references a binary-archive target ID (kind: release and generic-package).
	Archives string `yaml:"archives,omitempty"`

	// BinaryName overrides the binary name inside the archive (kind: binary-archive).
	// Auto-detected from referenced build if omitted.
	BinaryName string `yaml:"binary_name,omitempty"`

	// Format is the archive format: "tar.gz", "zip", "auto", or "binary" (kind:
	// binary-archive). "auto" picks zip for windows, tar.gz for everything else (default).
	// "binary" is a passthrough: the build's single-file output is carried as the
	// distributable as-is, NOT re-archived — for a build whose output is already packaged
	// (e.g. a kind: command build that emits its own tarball or a raw binary you want
	// uploaded unwrapped), so no double-archiving occurs.
	Format string `yaml:"format,omitempty"`

	// Name is the archive filename template (kind: binary-archive).
	// Supports: {id}, {version}, {os}, {arch}. e.g., "{id}-{version}-{os}-{arch}".
	Name string `yaml:"name,omitempty"`

	// Include lists extra files to bundle into the archive (kind: binary-archive).
	// e.g., ["README.md", "LICENSE"]
	Include []string `yaml:"include,omitempty"`

	// Checksums generates a SHA256SUMS file alongside archives (kind: binary-archive).
	Checksums bool `yaml:"checksums,omitempty"`

	// ── kind: generic-package ─────────────────────────────────────────────

	// Repo references a repos[].id (kind: generic-package). The forge identity
	// (provider, url, project, credentials) is resolved from the repo; the package
	// is published to that forge's generic package registry.
	Repo string `yaml:"repo,omitempty"`

	// Package is the generic package name (kind: generic-package).
	// Defaults to the repo project's basename if empty.
	Package string `yaml:"package,omitempty"`

	// Version is the immutable package version pattern (kind: generic-package).
	// Resolved against version info like Aliases ({version}, {sha:8}, ...), e.g.
	// "dev-{sha:8}". Published once, never overwritten. Distinct from a release Tag
	// (a git ref): this names a package *version*, not a git tag. Required — every
	// rolling Alias must have an immutable Version behind it.
	Version string `yaml:"version,omitempty"`

	// ── kind: pages ───────────────────────────────────────────────────────
	// Deploy a static site to a Pages host (Cloudflare/GitHub). Reuses Build (the
	// site build whose output tree is published), Provider (cloudflare|github),
	// Credentials (env-var prefix), When (release-gated), and Include.

	// Project is the Cloudflare Pages project name (provider: cloudflare). Default:
	// the target id. Cloudflare requires lowercase letters, digits, and hyphens,
	// 1–58 chars, no leading/trailing hyphen — validated at load. Ignored by the
	// github provider (which deploys to the gh-pages branch of the repo).
	Project string `yaml:"project,omitempty"`

	// Dir publishes a repo directory directly instead of a build's output tree
	// (kind: pages). Exactly one of Build or Dir must be set.
	Dir string `yaml:"dir,omitempty"`

	// Exclude drops matching paths from the publish workspace before deploy
	// (kind: pages). Globs, applied after extraction.
	Exclude []string `yaml:"exclude,omitempty"`

	// Domain is the custom domain(s) (kind: pages). Accepts a bare scalar or a list:
	//
	//	domain: precisionplanit.com
	//	domain: [precisionplanit.com, prplanit.com]
	//
	// Cloudflare attaches every listed domain to the project (each auto-wires DNS).
	// GitHub Pages serves a single custom domain, so a list is rejected at load; the
	// one entry is written as the CNAME.
	Domain StringOrList `yaml:"domain,omitempty"`

	// BasePath is the URL path the site is served under (kind: pages). Inferred per
	// provider (Cloudflare "/", GitHub project "/<repo>/") and fed into the build.
	BasePath string `yaml:"base_path,omitempty"`

	// Versioning controls multi-version publishing (kind: pages). P1 accepts only
	// mode: replace; mode: keep is reserved for phase 2 and rejected.
	Versioning *PagesVersioning `yaml:"versioning,omitempty"`
}

// PagesVersioning controls how released versions of a static site are published.
type PagesVersioning struct {
	// Mode: "replace" (each release overwrites; the only mode implemented in P1) or
	// "keep" (every released version stays browsable — reserved for phase 2, rejected
	// in P1 rather than silently ignored).
	Mode string `yaml:"mode,omitempty"`
}

// IsRemoteRelease returns true if this release target references a remote forge,
// either via explicit forge fields or via a mirror reference.
func (t TargetConfig) IsRemoteRelease() bool {
	if t.Mirror != "" {
		return true
	}
	return t.Provider != "" && t.URL != "" && t.ProjectID != "" && t.Credentials != ""
}

// TargetCondition defines routing conditions for a target.
// All non-empty fields must match (AND logic).
type TargetCondition struct {
	// Branches lists branch filters. Each entry is a policy name or "re:<regex>".
	// Empty = no branch filtering.
	Branches []string `yaml:"branches,omitempty"`

	// GitTags lists git tag filters. Each entry is a policy name or "re:<regex>".
	// Empty = no tag filtering.
	GitTags []string `yaml:"git_tags,omitempty"`

	// Events lists CI event type filters.
	// Supported: push, tag, release, schedule, manual, pull_request, merge_request.
	// Empty = no event filtering.
	Events []string `yaml:"events,omitempty"`

	// Forges restricts this target to specific CI forges by provider name
	// (github, gitlab, gitea, forgejo). Empty = every forge. Use it when a
	// registry is reachable/credentialed on some forges but not others — e.g. a
	// private mirror that only resolves from the self-hosted GitLab runner, or a
	// ghcr push that only makes sense on GitHub Actions.
	Forges []string `yaml:"forges,omitempty"`
}

// validTargetKinds enumerates all recognized target kinds.
var validTargetKinds = map[string]bool{
	"registry":         true,
	"docker-readme":    true,
	"gitlab-component": true,
	"release":          true,
	"binary-archive":   true,
	"generic-package":  true,
	"pages":            true,
}

// validArchiveFormats enumerates all recognized archive formats.
var validArchiveFormats = map[string]bool{
	"":       true, // default → "auto"
	"auto":   true,
	"tar.gz": true,
	"zip":    true,
	"binary": true, // passthrough: carry the build's file output as-is, no re-archiving
}

// validEvents enumerates all recognized event types.
var validEvents = map[string]bool{
	"push":          true,
	"tag":           true,
	"release":       true,
	"schedule":      true,
	"manual":        true,
	"pull_request":  true,
	"merge_request": true,
}
