package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const defaultConfigFile = ".stagefreight.yml"

// Config is the top-level StageFreight v2 configuration.
type Config struct {
	// Version must be 1. The pre-version config was an unversioned alpha
	// that never earned a schema number — this is the first stable schema.
	Version int `yaml:"version"`

	// Vars is a user-defined template variable dictionary.
	// Referenced as {var:name} anywhere templates are resolved.
	Vars map[string]string `yaml:"vars,omitempty"`

	// Defaults is inert YAML anchor storage. StageFreight ignores this
	// section entirely — it exists for users to define &anchors.
	Defaults yaml.Node `yaml:"defaults,omitempty"`

	// PresetSource is governance-distribution metadata: where a satellite's presets
	// were frozen from (forge coords + pinned ref + cache policy). Written by
	// `governance reconcile`; IGNORED at runtime today — the committed
	// .stagefreight/preset-cache is authoritative — but declared so a governed config
	// decodes under KnownFields(true). Consumed only if/when runtime pinned-external
	// resolution is built.
	PresetSource *PresetSource `yaml:"preset_source,omitempty"`

	// Forges declares git hosts as an id→forge map (provider, URL, credentials).
	Forges OrderedForges `yaml:"forges,omitempty"`

	// Repos declares projects as an id→repo map. References forges by id. Has role.
	Repos OrderedRepos `yaml:"repos,omitempty"`

	// Registries declares OCI registry hosts as an id→registry map.
	Registries OrderedRegistries `yaml:"registries,omitempty"`

	// SigningSetup is the signing block. Its Profiles field holds the named trust
	// profiles (generic primitives), referenced per-target by signing_profile: <id>.
	// "Releases require hardware" is project policy (the target's selection), never
	// encoded in the framework.
	SigningSetup SigningConfig `yaml:"signing,omitempty"`

	// Git is the git: cluster and the single source for ref interpretation: named
	// branch patterns (git.branches), tag patterns (git.tags), and versioning rules
	// (git.versioning). Consumers read cfg.Git.Branches / cfg.Git.Tags /
	// cfg.Git.Versioning.{BranchBuilds,NoLineage} directly — no translation layer.
	Git GitConfig `yaml:"git,omitempty"`

	// Builds defines named build artifacts as an id→build map.
	Builds OrderedBuilds `yaml:"builds"`

	// Targets defines distribution targets and side-effects. Declared under the
	// publish: key as an id→target map (execution order preserved). The retired
	// list form (targets:) no longer parses — upgrade via the config migrator.
	Targets OrderedTargets `yaml:"publish,omitempty"`

	// Lint holds lint-specific configuration (unchanged from v1).
	Lint LintConfig `yaml:"lint"`

	// Security holds security scanning configuration (unchanged from v1).
	Security SecurityConfig `yaml:"security"`

	// Commit holds configuration for the commit subsystem.
	Commit CommitConfig `yaml:"commit"`

	// Dependency holds configuration for the dependency update subsystem.
	Dependency DependencyConfig `yaml:"dependency"`

	// Narrate configures the Narrate phase (badges, patches, commit). Presence-enabled;
	// dissolves the old docs:/badges:/narrator: surface. Reference docs are a
	// kind: command build committed via narrate.commit.builds, not a subsystem here.
	Narrate NarrateConfig `yaml:"narrate"`

	Test TestConfig `yaml:"test"`

	// Manifest holds configuration for the manifest subsystem.
	Manifest ManifestConfig `yaml:"manifest"`

	// Release holds configuration for the release subsystem.
	Release ReleaseConfig `yaml:"release"`

	// CI holds all pipeline-related configuration consumed by ci render.
	CI CIConfig `yaml:"ci"`

	// Lifecycle defines the repository lifecycle mode (image, gitops, governance).
	Lifecycle LifecycleConfig `yaml:"lifecycle"`

	// Governance defines configuration for the governance lifecycle mode.
	// Only valid in the control repo (lifecycle.mode: governance).
	Governance GovernanceConfig `yaml:"governance"`

	// GitOps defines configuration for the gitops lifecycle mode.
	GitOps GitOpsConfig `yaml:"gitops"`

	// Docker defines configuration for the docker lifecycle mode.
	Docker DockerLifecycleConfig `yaml:"docker"`

	// BuildCache defines the build cache subsystem (local, shared, hybrid).
	BuildCache BuildCacheConfig `yaml:"build_cache"`

	// Glossary defines the repo's shared change-language model.
	// Consumed by commit authoring, tag planning, and release rendering.
	Glossary GlossaryConfig `yaml:"glossary"`

	// Tag holds workflow defaults for the tag planner.
	Tag TagConfig `yaml:"tagging"`

	// Toolchains defines operator control over external tool resolution.
	// Version pins, future retention policy, future trust settings.
	Toolchains ToolchainConfig `yaml:"toolchains,omitempty"`
}

// Load reads configuration from a YAML file.
// If path is empty, it tries the default file.
// Returns sensible defaults if the file doesn't exist.
// Discards validation warnings; use LoadWithWarnings for full diagnostics.
func Load(path string) (*Config, error) {
	cfg, _, err := LoadWithWarnings(path)
	return cfg, err
}

// PresetSource is governance-distribution metadata recording where a satellite's
// presets were frozen from — see the Config.PresetSource field. Present so a governed
// config decodes under KnownFields(true); ignored at runtime today.
type PresetSource struct {
	Provider    string `yaml:"provider,omitempty"`
	RepoURL     string `yaml:"repo_url,omitempty"`
	ProjectID   string `yaml:"project_id,omitempty"`
	Ref         string `yaml:"ref,omitempty"`
	CachePolicy string `yaml:"cache_policy,omitempty"`
}

// LoadWithWarnings reads configuration from a YAML file and returns validation
// warnings alongside the config. Thin wrapper over loadResolved — the single
// preset-resolving construction path (invariants.md §1); every runtime caller
// funnels through it.
func LoadWithWarnings(path string) (*Config, []string, error) {
	cfg, warnings, _, err := loadResolved(path)
	return cfg, warnings, err
}

// loadResolved is THE config construction path (invariants.md §1): it resolves
// per-section preset:/presets: on the RAW yaml map BEFORE the struct decode, so
// every RUNTIME caller (ci run phases, local build/reconcile via Load /
// LoadWithWarnings) gets preset-composed config — not just the offline reporter.
// It is the ONLY function that decodes yaml into a *Config; Load and LoadWithReport
// funnel through it (the reporter also consumes the returned provenance entries, so
// resolution happens exactly once).
//
// It reuses the existing resolver verbatim — ResolvePresets does the DeepMerge (local
// siblings override; maps deep-merge, scalars/lists replaced; presets: compose
// dedup-by-id), cycle/path-traversal guards, and provenance. Using the same
// localPresetLoader the reporter uses makes runtime resolve IDENTICALLY to the report
// (the split-brain repair). Presets load from the LOCAL committed cache
// (.stagefreight/preset-cache, cache-authoritative, no live fetch at build); the
// external policy repo is consulted only at governance distribution, which writes it.
func loadResolved(path string) (*Config, []string, []MergeEntry, error) {
	if path == "" {
		path = defaultConfigFile
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return defaults(), nil, nil, nil
		}
		return nil, nil, nil, err
	}

	var rawMap map[string]any
	if err := yaml.Unmarshal(data, &rawMap); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	// Resolve presets on the raw map. ResolvePresets both applies preset:/presets:
	// composition AND records provenance entries for every section (the entries feed
	// LoadWithReport, so it never resolves a second time). Same loader the reporter uses.
	absPath, _ := filepath.Abs(path)
	loader := localPresetLoader{baseDir: filepath.Dir(absPath)}
	resolvedMap, entries, rerr := ResolvePresets(rawMap, loader, "local", absPath, 0, nil)

	// Decode the RESOLVED map only when presets actually composed something; a
	// preset-free config decodes its ORIGINAL bytes verbatim (no map round-trip →
	// zero behavior change). A resolution failure only breaks the config when presets
	// are in play — a preset-free config loads regardless of any resolver hiccup.
	hasPresets := mapHasPresetKey(rawMap)
	decodeData := data
	if hasPresets {
		if rerr != nil {
			return nil, nil, nil, fmt.Errorf("resolving presets in %s: %w", path, rerr)
		}
		reenc, merr := yaml.Marshal(resolvedMap)
		if merr != nil {
			return nil, nil, nil, fmt.Errorf("re-encoding resolved %s: %w", path, merr)
		}
		decodeData = reenc
	} else if rerr != nil {
		entries = nil // resolver hiccup on a preset-free config: no usable provenance
	}

	cfg := defaults()
	dec := yaml.NewDecoder(bytes.NewReader(decodeData))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return nil, nil, entries, fmt.Errorf("parsing %s: %w", path, err)
	}

	warnings, verr := Validate(cfg)
	if verr != nil {
		return nil, warnings, entries, fmt.Errorf("validating %s: %w", path, verr)
	}

	// Fan registry: [a, b, c] into one single-registry target per id — after
	// validation (which sees the authored list), before normalization.
	cfg.Targets = expandMultiRegistryTargets(cfg.Targets)

	// Normalize: resolve all {var:...} templates throughout the entire config graph.
	// All consumers get fully-resolved values — no late binding.
	if err := Normalize(cfg); err != nil {
		return nil, warnings, entries, fmt.Errorf("normalizing %s: %w", path, err)
	}

	// Hard assertion: no {var:} may survive normalization.
	if err := AssertNormalized(cfg); err != nil {
		return nil, warnings, entries, fmt.Errorf("normalizing %s: %w", path, err)
	}

	return cfg, warnings, entries, nil
}

// mapHasPresetKey reports whether a decoded config map carries any preset:/presets:
// key at any depth — the cheap gate that keeps a preset-free config on its exact
// original decode path (resolve for provenance still runs; only the decode source
// stays the untouched bytes).
func mapHasPresetKey(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if _, ok := t["preset"]; ok {
			return true
		}
		if _, ok := t["presets"]; ok {
			return true
		}
		for _, vv := range t {
			if mapHasPresetKey(vv) {
				return true
			}
		}
	case []any:
		for _, vv := range t {
			if mapHasPresetKey(vv) {
				return true
			}
		}
	}
	return false
}

func defaults() *Config {
	return &Config{
		Version:    1,
		Vars:       map[string]string{},
		Lint:       DefaultLintConfig(),
		Security:   DefaultSecurityConfig(),
		Commit:     DefaultCommitConfig(),
		Dependency: DefaultDependencyConfig(),
		Test:       DefaultTestConfig(),
		Manifest:   DefaultManifestConfig(),
		Release:    DefaultReleaseConfig(),
		GitOps:     DefaultGitOpsConfig(),
		BuildCache: DefaultBuildCacheConfig(),
		Docker:     DefaultDockerLifecycleConfig(),
		Glossary:   DefaultGlossaryConfig(),
		Tag:        DefaultTagConfig(),
	}
}
