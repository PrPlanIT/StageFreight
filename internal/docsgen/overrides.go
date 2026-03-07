package docsgen

// SectionOverride provides framing and context for a top-level config key.
type SectionOverride struct {
	Summary string   // what this section governs
	Example string   // inline YAML snippet
	Notes   []string // gotchas, special semantics
}

// FieldOverride provides per-field documentation enrichment.
type FieldOverride struct {
	Description   string
	AllowedValues []string
	Default       string
	Required      *bool    // override omitempty heuristic
	Example       string
	Notes         []string
}

func boolPtr(v bool) *bool { return &v }

// sectionOverrides maps top-level config keys to curated documentation.
var sectionOverrides = map[string]SectionOverride{
	"version": {
		Summary: "Schema version number. Must be `1` — the first stable schema.",
		Example: "version: 1",
	},
	"vars": {
		Summary: "User-defined template variable dictionary. Referenced as `{var:name}` anywhere templates are resolved.",
		Example: `vars:
  org: prplanit
  repo: stagefreight`,
	},
	"sources": {
		Summary: "Build source configuration. Defines where source code lives and how to derive link/raw URLs.",
		Example: `sources:
  primary:
    kind: git
    worktree: "."
    url: "https://github.com/myorg/myrepo"
    default_branch: "main"`,
	},
	"policies": {
		Summary: "Named regex patterns for git tag and branch matching. Referenced by target `when` conditions (e.g., `git_tags: [stable]`).",
		Example: `policies:
  git_tags:
    stable: "^v?\\d+\\.\\d+\\.\\d+$"
    prerelease: "^v?\\d+\\.\\d+\\.\\d+-.+"
  branches:
    main: "^main$"`,
	},
	"builds": {
		Summary: "Named build artifacts. Each build has a unique ID referenced by targets. Currently supports `kind: docker`.",
		Example: `builds:
  - id: myapp
    kind: docker
    platforms: [linux/amd64, linux/arm64]`,
		Notes: []string{
			"Build IDs must be unique across all builds.",
			"Targets reference builds by name via the `build:` field.",
		},
	},
	"targets": {
		Summary: "Distribution targets and side-effects. Each target has a `kind` that determines its behavior: push images, sync READMEs, publish components, or create releases.",
		Example: `targets:
  - id: dockerhub-stable
    kind: registry
    build: myapp
    url: docker.io
    path: myorg/myapp
    tags: ["{version}", "latest"]
    when: { git_tags: [stable], events: [tag] }
    credentials: DOCKER`,
		Notes: []string{
			"Target IDs must be unique across all targets.",
			"The `when` block controls routing: all non-empty fields must match (AND logic).",
		},
	},
	"narrator": {
		Summary: "Content composition for file targets. Composes badges, shields, text, includes, and components into managed `<!-- sf:markers -->` sections in any file.",
		Example: `narrator:
  - file: "README.md"
    link_base: "https://github.com/myorg/myrepo/blob/main"
    items:
      - id: badge.release
        kind: badge
        placement:
          between: ["<!-- sf:badges:start -->", "<!-- sf:badges:end -->"]
          mode: replace
          inline: true
        text: release
        output: ".stagefreight/badges/release.svg"`,
	},
	"commit": {
		Summary: "Commit subsystem configuration. Controls conventional commit formatting, type registry, and default behavior for `stagefreight commit`.",
		Example: `commit:
  default_type: docs
  conventional: true
  backend: git
  skip_ci: false
  push: false
  types:
    - key: feat
      label: Feature
    - key: breaking
      label: Breaking
      force_bang: true`,
	},
	"lint": {
		Summary: "Linting configuration. Controls scan mode, module toggles, and per-module options. 9 modules: tabs, secrets, conflicts, filesize, linecount, unicode, yaml, lineendings, freshness.",
		Example: `lint:
  level: changed
  modules:
    secrets:
      enabled: true
    freshness:
      enabled: true
      options:
        cache_ttl: 300`,
	},
	"security": {
		Summary: "Security scanning configuration. Controls vulnerability scanning (Trivy, Grype), SBOM generation (Syft), and how security info appears in release notes.",
		Example: `security:
  enabled: true
  scanners:
    trivy: true
    grype: true
  sbom: true
  release_detail: counts`,
	},
}

// fieldOverrides maps docs-path keys to curated field documentation.
var fieldOverrides = map[string]FieldOverride{
	// ── sources ──────────────────────────────────────────────────────────
	"sources.primary.kind": {
		Description: "Source type.",
		Default:     "git",
	},
	"sources.primary.worktree": {
		Description: "Path to working tree.",
		Default:     `"."`,
	},
	"sources.primary.url": {
		Description: "Repository URL. Enables deterministic link_base/raw_base derivation.",
	},
	"sources.primary.default_branch": {
		Description: "Default branch name. Used with URL for raw_base derivation.",
	},

	// ── builds ───────────────────────────────────────────────────────────
	"builds.id": {
		Description: "Unique identifier for this build, referenced by targets.",
		Required:    boolPtr(true),
	},
	"builds.kind": {
		Description:   "Build type. Determines which fields are valid.",
		AllowedValues: []string{"docker"},
		Required:      boolPtr(true),
	},
	"builds.build_mode": {
		Description:   "Build execution strategy.",
		AllowedValues: []string{"(standard)", "crucible"},
		Default:       "(standard)",
		Notes:         []string{"Crucible mode performs a self-proving rebuild to verify build reproducibility."},
	},
	"builds.dockerfile": {
		Description: "Path to the Dockerfile.",
		Default:     "auto-detected",
	},
	"builds.context": {
		Description: "Docker build context path.",
		Default:     `"."`,
	},
	"builds.target": {
		Description: "Multi-stage build `--target` stage name.",
	},
	"builds.platforms": {
		Description: "Target platforms for multi-arch builds.",
		Default:     "current OS/arch",
		Example:     `[linux/amd64, linux/arm64]`,
	},
	"builds.build_args": {
		Description: "Key-value pairs passed as `--build-arg`. Supports template variables.",
	},
	"builds.select_tags": {
		Description: "Tags for CLI filtering via `--select`.",
	},

	// ── builds.cache ────────────────────────────────────────────────────
	"builds.cache.watch.paths": {
		Description: "Glob patterns for files to watch for changes.",
	},
	"builds.cache.watch.invalidates": {
		Description: "Build stage names invalidated when watched files change.",
	},
	"builds.cache.auto_detect": {
		Description: "Auto-detect cache-relevant files from Dockerfile COPY/ADD instructions.",
		Default:     "true",
	},

	// ── targets ──────────────────────────────────────────────────────────
	"targets.id": {
		Description: "Unique identifier for this target (logging, status, enable/disable).",
		Required:    boolPtr(true),
	},
	"targets.kind": {
		Description:   "Target type. Determines which fields are valid.",
		AllowedValues: []string{"registry", "docker-readme", "gitlab-component", "release"},
		Required:      boolPtr(true),
	},
	"targets.build": {
		Description: "References a `builds[].id`. Required for `kind: registry`.",
	},
	"targets.url": {
		Description: "Registry or forge hostname.",
		Example:     "docker.io",
	},
	"targets.provider": {
		Description:   "Vendor type for auth and API behavior. Auto-detected from URL if omitted on registry/docker-readme targets.",
		AllowedValues: []string{"docker", "ghcr", "gitlab", "jfrog", "harbor", "quay", "gitea", "forgejo", "generic", "github"},
	},
	"targets.path": {
		Description: "Image path within the registry.",
		Example:     "myorg/myapp",
	},
	"targets.credentials": {
		Description: "Env var prefix for authentication. Resolution: try `{PREFIX}_TOKEN` first, else `{PREFIX}_USER` + `{PREFIX}_PASS`.",
	},
	"targets.tags": {
		Description: "Tag templates resolved against version info. `kind: registry` only.",
		Example:     `["{version}", "{major}.{minor}", "latest"]`,
	},
	"targets.aliases": {
		Description: "Rolling git tag aliases. `kind: release` only.",
		Example:     `["{version}", "{major}.{minor}", "latest"]`,
	},
	"targets.when.git_tags": {
		Description: "Git tag filters. Each entry is a policy name or inline regex.",
	},
	"targets.when.branches": {
		Description: "Branch filters. Each entry is a policy name or inline regex.",
	},
	"targets.when.events": {
		Description:   "CI event type filters.",
		AllowedValues: []string{"push", "tag", "release", "schedule", "manual", "pull_request", "merge_request"},
	},
	"targets.retention": {
		Description: "Tag/release cleanup policy. Accepts integer (shorthand for `keep_last`) or policy map. Restic-style additive rules.",
	},
	"targets.retention.keep_last": {
		Description: "Keep the N most recent tags/releases.",
	},
	"targets.retention.keep_daily": {
		Description: "Keep one per day for the last N days.",
	},
	"targets.retention.keep_weekly": {
		Description: "Keep one per week for the last N weeks.",
	},
	"targets.retention.keep_monthly": {
		Description: "Keep one per month for the last N months.",
	},
	"targets.retention.keep_yearly": {
		Description: "Keep one per year for the last N years.",
	},
	"targets.retention.protect": {
		Description: "Tag patterns that are never deleted.",
	},
	"targets.file": {
		Description: "Path to the README file. `kind: docker-readme` only.",
	},
	"targets.link_base": {
		Description: "Base URL for relative link rewriting. `kind: docker-readme` only.",
	},
	"targets.spec_files": {
		Description: "Component spec file paths. `kind: gitlab-component` only.",
	},
	"targets.catalog": {
		Description: "Enable GitLab Catalog registration. `kind: gitlab-component` only.",
	},
	"targets.project_id": {
		Description: "Project identifier (`owner/repo` or numeric ID). `kind: release`, remote targets only.",
	},
	"targets.sync_release": {
		Description: "Sync release notes + tags to a remote forge. `kind: release`, remote targets only.",
	},
	"targets.sync_assets": {
		Description: "Sync scan artifacts to a remote forge. `kind: release`, remote targets only.",
	},

	// ── narrator ─────────────────────────────────────────────────────────
	"narrator.file": {
		Description: "Path to the target file.",
		Required:    boolPtr(true),
	},
	"narrator.link_base": {
		Description: "Base URL for relative link rewriting. `raw_base` is auto-derived from this.",
	},
	"narrator.items": {
		Description: "Composable content items for this file.",
	},
	"narrator.items.id": {
		Description: "Item identifier, unique within file.",
	},
	"narrator.items.kind": {
		Description:   "Item type. Determines which fields are valid.",
		AllowedValues: []string{"badge", "shield", "text", "component", "break", "include"},
		Required:      boolPtr(true),
	},
	"narrator.items.placement.between": {
		Description: "Two-element array: `[start_marker, end_marker]`. Content is placed between these markers.",
	},
	"narrator.items.placement.mode": {
		Description:   "How content is placed relative to markers.",
		AllowedValues: []string{"replace", "append", "prepend", "above", "below"},
		Default:       "replace",
	},
	"narrator.items.placement.inline": {
		Description: "Render items side-by-side (space-joined) when true. Default: block (newline-joined).",
		Default:     "false",
	},
	"narrator.items.text": {
		Description: "Badge label (left side text). `kind: badge` only.",
	},
	"narrator.items.value": {
		Description: "Badge value (right side text, supports templates). `kind: badge` only.",
	},
	"narrator.items.color": {
		Description: "Badge color as hex or `auto` (status-driven). `kind: badge` only.",
	},
	"narrator.items.output": {
		Description: "SVG output path for badge generation. `kind: badge` only.",
	},
	"narrator.items.link": {
		Description: "Clickable URL. `kind: badge` and `kind: shield`.",
	},
	"narrator.items.shield": {
		Description: "Shields.io path (appended to `https://img.shields.io/`). `kind: shield` only.",
	},
	"narrator.items.content": {
		Description: "Raw text/markdown content. Supports template variables. `kind: text` only.",
	},
	"narrator.items.spec": {
		Description: "Component spec file path. `kind: component` only.",
	},
	"narrator.items.path": {
		Description: "File path to include verbatim. `kind: include` only.",
	},

	// ── commit ───────────────────────────────────────────────────────────
	"commit.default_type": {
		Description: "Default commit type when --type is omitted.",
		Default:     "chore",
	},
	"commit.default_scope": {
		Description: "Default commit scope when --scope is omitted.",
	},
	"commit.skip_ci": {
		Description: "Append `[skip ci]` to commit subjects by default.",
		Default:     "false",
	},
	"commit.push": {
		Description: "Push after committing by default.",
		Default:     "false",
	},
	"commit.conventional": {
		Description: "Use conventional commit format (`type(scope): summary`).",
		Default:     "true",
	},
	"commit.backend": {
		Description:   "Commit execution backend.",
		AllowedValues: []string{"git", "dry-run"},
		Default:       "git",
	},
	"commit.types": {
		Description: "Recognized commit types for validation and alias resolution.",
	},
	"commit.types.key": {
		Description: "Type identifier used in `--type` flag. Must match `^[a-z][a-z0-9_-]*$`.",
		Required:    boolPtr(true),
	},
	"commit.types.label": {
		Description: "Human-readable label for documentation and error messages.",
	},
	"commit.types.alias_for": {
		Description: "Resolve this type to another type key. No alias chains allowed.",
	},
	"commit.types.force_bang": {
		Description: "Force breaking change indicator (`!`) when this type is used.",
		Default:     "false",
	},

	// ── lint ──────────────────────────────────────────────────────────────
	"lint.level": {
		Description:   "Scan mode. `changed` scans only modified files; `full` scans everything.",
		AllowedValues: []string{"changed", "full"},
		Default:       "changed",
	},
	"lint.cache_dir": {
		Description: "Override cache directory.",
		Default:     "$XDG_CACHE_HOME/stagefreight/<repo-hash>/lint",
	},
	"lint.target_branch": {
		Description: "Target branch for diff-based scanning.",
	},
	"lint.exclude": {
		Description: "Glob patterns to exclude from lint scanning.",
	},
	"lint.modules": {
		Description: "Per-module configuration. Keys: tabs, secrets, conflicts, filesize, linecount, unicode, yaml, lineendings, freshness.",
	},

	// ── security ─────────────────────────────────────────────────────────
	"security.enabled": {
		Description: "Run vulnerability scanning.",
		Default:     "true",
	},
	"security.scanners": {
		Description: "Per-scanner toggles.",
	},
	"security.scanners.trivy": {
		Description: "Run Trivy image scan.",
		Default:     "true",
	},
	"security.scanners.grype": {
		Description: "Run Grype image scan.",
		Default:     "true",
	},
	"security.sbom": {
		Description: "Generate SBOM artifacts via Syft.",
		Default:     "true",
	},
	"security.fail_on_critical": {
		Description: "Fail the pipeline if critical vulnerabilities are found.",
		Default:     "false",
	},
	"security.output_dir": {
		Description: "Directory for scan artifacts (JSON, SARIF, SBOM, summary).",
		Default:     ".stagefreight/security",
	},
	"security.release_detail": {
		Description:   "Default detail level for security info in release notes.",
		AllowedValues: []string{"none", "counts", "detailed", "full"},
		Default:       "counts",
	},
	"security.release_detail_rules": {
		Description: "Conditional detail level overrides. Evaluated top-down, first match wins. Uses the Condition primitive.",
	},
	"security.release_detail_rules.tag": {
		Description: "Git tag pattern to match. Prefix with `!` to negate.",
	},
	"security.release_detail_rules.branch": {
		Description: "Branch pattern to match. Prefix with `!` to negate.",
	},
	"security.release_detail_rules.detail": {
		Description:   "Detail level when this rule matches.",
		AllowedValues: []string{"none", "counts", "detailed", "full"},
	},
	"security.overwhelm_message": {
		Description: "Message lines shown when >1000 vulnerabilities are found.",
		Default:     `["…maybe start here:"]`,
	},
	"security.overwhelm_link": {
		Description: "URL appended after overwhelm message. Empty string disables.",
	},
}
