package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	depversion "github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// Validate checks structural invariants of a loaded v2 Config.
// Returns warnings (soft issues) and a hard error if the config is invalid.
func Validate(cfg *Config) (warnings []string, err error) {
	var errs []string

	// ── Version ───────────────────────────────────────────────────────────

	if cfg.Version != 1 {
		errs = append(errs, fmt.Sprintf("version: must be 1, got %d", cfg.Version))
	}

	// ── Versioning ───────────────────────────────────────────────────────

	// tag_sources: unique ids, non-empty patterns, valid identifiers.
	tagSourceIDs := make(map[string]bool, len(cfg.Versioning.TagSources))
	for i, ts := range cfg.Versioning.TagSources {
		tspath := fmt.Sprintf("versioning.tag_sources[%d]", i)
		if ts.ID == "" {
			errs = append(errs, fmt.Sprintf("%s: id is required", tspath))
			continue
		}
		if !isIdentifier(ts.ID) {
			errs = append(errs, fmt.Sprintf("%s: id %q is not a valid identifier (must match [a-zA-Z][a-zA-Z0-9_.\\-]*)", tspath, ts.ID))
		}
		if tagSourceIDs[ts.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate id %q", tspath, ts.ID))
		}
		tagSourceIDs[ts.ID] = true
		if ts.Pattern == "" {
			errs = append(errs, fmt.Sprintf("%s: pattern is required", tspath))
		} else if _, rerr := regexp.Compile(ts.Pattern); rerr != nil {
			errs = append(errs, fmt.Sprintf("%s: pattern %q is not a valid regex: %v", tspath, ts.Pattern, rerr))
		}
	}

	// branch_builds: ordered slice validation.
	// - id required, unique
	// - default required and last
	// - non-default requires match + match references a declared branch matcher
	// - default forbids match
	// - base_from non-empty, every id exists in tag_sources, no duplicates inside
	// - format non-empty
	// - two non-default entries must not share the same match
	branchBuildIDs := make(map[string]bool, len(cfg.Versioning.BranchBuilds))
	matchToRuleID := make(map[string]string) // match → branch_build id (for duplicate detection)
	hasDefault := false
	defaultIndex := -1
	for i, bb := range cfg.Versioning.BranchBuilds {
		bbpath := fmt.Sprintf("versioning.branch_builds[%d]", i)
		if bb.ID == "" {
			errs = append(errs, fmt.Sprintf("%s: id is required", bbpath))
			continue
		}
		if branchBuildIDs[bb.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate id %q", bbpath, bb.ID))
		}
		branchBuildIDs[bb.ID] = true

		if bb.ID == "default" {
			hasDefault = true
			defaultIndex = i
			if bb.Match != "" {
				errs = append(errs, fmt.Sprintf("%s: default entry must not have match (default catches unmatched branches)", bbpath))
			}
		} else {
			if bb.Match == "" {
				errs = append(errs, fmt.Sprintf("%s: match is required for non-default entries", bbpath))
			} else if _, ok := cfg.Matchers.Branches[bb.Match]; !ok {
				errs = append(errs, fmt.Sprintf("%s: match %q not found in matchers.branches", bbpath, bb.Match))
			} else {
				// duplicate-match detection: two non-default rules must not share the same match
				if prev, seen := matchToRuleID[bb.Match]; seen {
					errs = append(errs, fmt.Sprintf("%s: match %q is already used by branch_build %q (1:1 mapping required; first match wins creates implicit priority otherwise)", bbpath, bb.Match, prev))
				} else {
					matchToRuleID[bb.Match] = bb.ID
				}
			}
		}

		if len(bb.BaseFrom) == 0 {
			errs = append(errs, fmt.Sprintf("%s: base_from is required (non-empty list of tag_sources ids)", bbpath))
		} else {
			seen := make(map[string]bool, len(bb.BaseFrom))
			for _, id := range bb.BaseFrom {
				if seen[id] {
					errs = append(errs, fmt.Sprintf("%s: duplicate base_from entry %q", bbpath, id))
				}
				seen[id] = true
				if !tagSourceIDs[id] {
					errs = append(errs, fmt.Sprintf("%s: base_from id %q not found in versioning.tag_sources", bbpath, id))
				}
			}
		}

		if bb.Format == "" {
			errs = append(errs, fmt.Sprintf("%s: format is required", bbpath))
		}
	}

	if len(cfg.Versioning.BranchBuilds) > 0 {
		if !hasDefault {
			errs = append(errs, "versioning.branch_builds: an entry with id 'default' is required (catch-all for unmatched branches)")
		} else if defaultIndex != len(cfg.Versioning.BranchBuilds)-1 {
			errs = append(errs, fmt.Sprintf("versioning.branch_builds: default entry must be last (found at index %d of %d)", defaultIndex, len(cfg.Versioning.BranchBuilds)))
		}
	}

	switch cfg.Versioning.NoLineage.Mode {
	case "", "error":
		// valid
	case "explicit":
		v := cfg.Versioning.NoLineage.Version
		if v == "" {
			errs = append(errs, "versioning.no_lineage: mode explicit requires version template")
		} else if !strings.Contains(v, "{sha}") && !strings.Contains(v, "{time}") {
			errs = append(errs, "versioning.no_lineage: version must contain {sha} or {time} (hardcoded versions are dishonest)")
		}
	default:
		errs = append(errs, fmt.Sprintf("versioning.no_lineage: unknown mode %q (expected error or explicit)", cfg.Versioning.NoLineage.Mode))
	}

	// ── Matchers ────────────────────────────────────────────────────────

	for name, pattern := range cfg.Matchers.Branches {
		if !isIdentifier(name) {
			errs = append(errs, fmt.Sprintf("matchers.branches: key %q is not a valid identifier (must match [a-zA-Z][a-zA-Z0-9_.\\-]*)", name))
		}
		if pattern == "" {
			errs = append(errs, fmt.Sprintf("matchers.branches[%s]: pattern is required", name))
		} else if _, rerr := regexp.Compile(pattern); rerr != nil {
			errs = append(errs, fmt.Sprintf("matchers.branches[%s]: pattern %q is not a valid regex: %v", name, pattern, rerr))
		}
	}

	// ── Builds ────────────────────────────────────────────────────────────

	buildIDs := make(map[string]bool)
	for i, b := range cfg.Builds {
		bpath := fmt.Sprintf("builds[%d]", i)

		if b.ID == "" {
			errs = append(errs, fmt.Sprintf("%s: id is required", bpath))
		} else if buildIDs[b.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate build id %q", bpath, b.ID))
		} else {
			buildIDs[b.ID] = true
		}

		if b.Kind == "" {
			errs = append(errs, fmt.Sprintf("%s: kind is required", bpath))
		} else if b.Kind != "docker" && b.Kind != "binary" && b.Kind != "command" {
			errs = append(errs, fmt.Sprintf("%s: unknown build kind %q (supported: docker, binary, command)", bpath, b.Kind))
		}

		// kind: command — the escape hatch: an explicit command + ≥1 typed output.
		if b.Kind == "command" {
			if strings.TrimSpace(string(b.Command)) == "" {
				errs = append(errs, fmt.Sprintf("%s: kind command requires command (the command to run in the image)", bpath))
			}
			if len(b.Outputs) == 0 {
				errs = append(errs, fmt.Sprintf("%s: kind command requires at least one output ({type, source})", bpath))
			}
			for i, o := range b.Outputs {
				switch o.Type {
				case "tree", "file", "binary":
				default:
					errs = append(errs, fmt.Sprintf("%s: outputs[%d].type %q is invalid (supported: tree, file, binary)", bpath, i, o.Type))
				}
				if strings.TrimSpace(o.Source) == "" {
					errs = append(errs, fmt.Sprintf("%s: outputs[%d].source is required (the path the command produced, repo-relative)", bpath, i))
				} else if strings.HasPrefix(o.Source, "/") || strings.Contains(o.Source, "..") {
					errs = append(errs, fmt.Sprintf("%s: outputs[%d].source %q must be repo-relative (no leading / or ..)", bpath, i, o.Source))
				}
			}
			if b.Builder != "" {
				errs = append(errs, fmt.Sprintf("%s: builder is not valid for kind command (it has no language inference)", bpath))
			}
		}

		// DependsOn reference validation (deferred until all IDs collected)
		// Binary-specific validation
		if b.Kind == "binary" {
			switch b.Builder {
			case "":
				errs = append(errs, fmt.Sprintf("%s: kind binary requires builder (supported: go, rust, node, elixir, dotnet, c, python, jvm, android)", bpath))
			case "go", "rust", "node", "elixir", "dotnet", "c", "python", "jvm", "android":
				// Symmetric: builder owns the build, config supplies `from`. The
				// containerized builders infer image/command/output from convention
				// (override optional; c with a raw Makefile needs output: since the
				// artifact location is unknowable; android signs via CI-secret env vars).
				if b.From == "" {
					errs = append(errs, fmt.Sprintf("%s: kind binary requires from (go: source package; rust: the crate dir with Cargo.toml; node: the package dir; elixir: the mix project dir; dotnet: the project/solution dir; c: the source dir; python: the project dir; jvm: the gradle/maven project dir; android: the gradle project root)", bpath))
				}
			default:
				errs = append(errs, fmt.Sprintf("%s: unknown builder %q (supported: go, rust, node, elixir, dotnet, c, python, jvm, android)", bpath, b.Builder))
			}
			// Docker-only fields should not be set on binary builds
			if b.Dockerfile != "" {
				errs = append(errs, fmt.Sprintf("%s: dockerfile is not valid for kind binary", bpath))
			}
			if b.Context != "" {
				errs = append(errs, fmt.Sprintf("%s: context is not valid for kind binary", bpath))
			}
			if b.Target != "" {
				errs = append(errs, fmt.Sprintf("%s: target is not valid for kind binary", bpath))
			}
			if len(b.BuildArgs) > 0 {
				errs = append(errs, fmt.Sprintf("%s: build_args is not valid for kind binary (use args)", bpath))
			}
			if b.Stage != nil {
				errs = append(errs, fmt.Sprintf("%s: stage is not valid for kind binary (it recycles a binary into a docker build)", bpath))
			}
		}

		// Docker-only: binary fields should not be set
		if b.Kind == "docker" {
			if b.Builder != "" {
				errs = append(errs, fmt.Sprintf("%s: builder is not valid for kind docker", bpath))
			}
			if b.From != "" {
				errs = append(errs, fmt.Sprintf("%s: from is not valid for kind docker", bpath))
			}
			if len(b.Args) > 0 {
				errs = append(errs, fmt.Sprintf("%s: args is not valid for kind docker", bpath))
			}
			if len(b.Env) > 0 {
				errs = append(errs, fmt.Sprintf("%s: env is not valid for kind docker", bpath))
			}
			if b.Stage != nil {
				if b.Stage.From == "" {
					errs = append(errs, fmt.Sprintf("%s: stage requires from (the binary build id to recycle)", bpath))
				}
				if b.Stage.As == "" {
					errs = append(errs, fmt.Sprintf("%s: stage requires as (the context path, e.g. \"app-{arch}\")", bpath))
				}
			}
		}

		if b.BuildMode != "" && b.BuildMode != "crucible" {
			errs = append(errs, fmt.Sprintf("%s: unknown build_mode %q (supported: crucible)", bpath, b.BuildMode))
		}
	}

	// ── Build depends_on validation (all IDs now known) ─────────────────

	for i, b := range cfg.Builds {
		if b.DependsOn != "" {
			bpath := fmt.Sprintf("builds[%d]", i)
			if !buildIDs[b.DependsOn] {
				errs = append(errs, fmt.Sprintf("%s: depends_on references unknown build %q", bpath, b.DependsOn))
			}
			if b.DependsOn == b.ID {
				errs = append(errs, fmt.Sprintf("%s: depends_on cannot reference itself", bpath))
			}
		}
		if b.Stage != nil && b.Stage.From != "" && !buildIDs[b.Stage.From] {
			errs = append(errs, fmt.Sprintf("builds[%d]: stage.from references unknown build %q", i, b.Stage.From))
		}
	}

	// ── Targets ───────────────────────────────────────────────────────────

	targetIDs := make(map[string]bool)
	for i, t := range cfg.Targets {
		tpath := fmt.Sprintf("targets[%d]", i)

		if t.ID == "" {
			errs = append(errs, fmt.Sprintf("%s: id is required", tpath))
		} else if targetIDs[t.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate target id %q", tpath, t.ID))
		} else {
			targetIDs[t.ID] = true
		}

		if t.Kind == "" {
			errs = append(errs, fmt.Sprintf("%s: kind is required", tpath))
		} else if !validTargetKinds[t.Kind] {
			kinds := make([]string, 0, len(validTargetKinds))
			for k := range validTargetKinds {
				kinds = append(kinds, k)
			}
			errs = append(errs, fmt.Sprintf("%s: unknown target kind %q (supported: %s)", tpath, t.Kind, strings.Join(kinds, ", ")))
		}

		// Build reference validation
		if t.Build != "" && !buildIDs[t.Build] {
			errs = append(errs, fmt.Sprintf("%s: references unknown build %q", tpath, t.Build))
		}

		// Kind-specific validation
		terrs := validateTarget(t, tpath, buildIDs, cfg.Matchers, cfg.Registries)
		errs = append(errs, terrs...)

		// When block validation
		werrs := validateWhen(t.When, tpath, cfg.Versioning, cfg.Matchers)
		errs = append(errs, werrs...)
	}

	// ── Narrate: badges ──────────────────────────────────────────────────

	badgeIDs := make(map[string]bool)
	for i, b := range cfg.Narrate.Badges {
		bpath := fmt.Sprintf("narrate.badges[%d]", i)
		if b.ID == "" {
			errs = append(errs, fmt.Sprintf("%s: id is required", bpath))
		} else if badgeIDs[b.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate badge id %q", bpath, b.ID))
		} else {
			badgeIDs[b.ID] = true
		}
		if b.Output == "" {
			errs = append(errs, fmt.Sprintf("%s: output is required", bpath))
		}
		if b.Value == "" {
			errs = append(errs, fmt.Sprintf("%s: value is required", bpath))
		}
		if b.Text == "" {
			errs = append(errs, fmt.Sprintf("%s: text is required", bpath))
		}
	}

	// ── Narrate: patches ─────────────────────────────────────────────────

	for fi, f := range cfg.Narrate.Patches {
		fpath := fmt.Sprintf("narrate.patches[%d]", fi)

		if f.File == "" {
			errs = append(errs, fmt.Sprintf("%s: file is required", fpath))
		}

		itemIDs := make(map[string]bool)
		for ii, item := range f.Items {
			ipath := fmt.Sprintf("%s.items[%d]", fpath, ii)

			if item.ID != "" {
				if itemIDs[item.ID] {
					errs = append(errs, fmt.Sprintf("%s: duplicate item id %q", ipath, item.ID))
				}
				itemIDs[item.ID] = true
			}

			ierrs := validateNarratorItem(item, ipath, buildIDs)
			errs = append(errs, ierrs...)
		}
	}

	// ── Commit ────────────────────────────────────────────────────────────

	commitTypeKeys := make(map[string]bool)
	commitTypeKeyRe := regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	for i, ct := range cfg.Commit.Types {
		cpath := fmt.Sprintf("commit.types[%d]", i)

		if ct.Key == "" {
			errs = append(errs, fmt.Sprintf("%s: key is required", cpath))
			continue
		}
		if !commitTypeKeyRe.MatchString(ct.Key) {
			errs = append(errs, fmt.Sprintf("%s: key %q must match ^[a-z][a-z0-9_-]*$", cpath, ct.Key))
		}
		if commitTypeKeys[ct.Key] {
			errs = append(errs, fmt.Sprintf("%s: duplicate key %q", cpath, ct.Key))
		} else {
			commitTypeKeys[ct.Key] = true
		}

		if ct.AliasFor != "" {
			if !commitTypeKeys[ct.AliasFor] {
				// Check forward: is target defined later?
				found := false
				for _, other := range cfg.Commit.Types {
					if other.Key == ct.AliasFor {
						found = true
						break
					}
				}
				if !found {
					errs = append(errs, fmt.Sprintf("%s: alias_for %q references unknown type", cpath, ct.AliasFor))
				}
			}
			// Check alias doesn't target another alias (no chains)
			for _, other := range cfg.Commit.Types {
				if other.Key == ct.AliasFor && other.AliasFor != "" {
					errs = append(errs, fmt.Sprintf("%s: alias_for %q targets another alias (chains not allowed)", cpath, ct.AliasFor))
				}
			}
		}
	}

	// ── Dependency ───────────────────────────────────────────────────────

	if cfg.Dependency.Output != "" {
		if pathErrs := validateOutputPath(cfg.Dependency.Output, "dependency.output"); len(pathErrs) > 0 {
			errs = append(errs, pathErrs...)
		}
	}
	if cfg.Dependency.Commit.Type != "" {
		commitTypeKeyRe2 := regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
		if !commitTypeKeyRe2.MatchString(cfg.Dependency.Commit.Type) {
			errs = append(errs, fmt.Sprintf("dependency.commit.type: %q must match ^[a-z][a-z0-9_-]*$", cfg.Dependency.Commit.Type))
		}
	}
	if cfg.Dependency.Enabled {
		if !cfg.Dependency.Scope.GoModules && !cfg.Dependency.Scope.DockerfileEnv {
			errs = append(errs, "dependency: at least one scope must be true when enabled")
		}
		if cfg.Dependency.Commit.Enabled && cfg.Dependency.Commit.Message == "" {
			errs = append(errs, "dependency.commit: message is required when commit enabled")
		}
	}
	if p := cfg.Dependency.Commit.Promotion; p != "" && p != PromotionDirect && p != PromotionMR {
		errs = append(errs, fmt.Sprintf("dependency.commit.promotion: %q is invalid (expected %q or %q)", p, PromotionDirect, PromotionMR))
	}
	if cfg.Dependency.Commit.Promotion == PromotionMR && !cfg.Dependency.Commit.Push {
		errs = append(errs, "dependency.commit: promotion \"mr\" requires push to be true (no remote branch means no merge request)")
	}

	// ── Narrate: commit ──────────────────────────────────────────────────

	if cfg.Narrate.Commit.Type != "" {
		commitTypeKeyRe3 := regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
		if !commitTypeKeyRe3.MatchString(cfg.Narrate.Commit.Type) {
			errs = append(errs, fmt.Sprintf("narrate.commit.type: %q must match ^[a-z][a-z0-9_-]*$", cfg.Narrate.Commit.Type))
		}
	}
	for i, p := range cfg.Narrate.Commit.Add {
		if pathErrs := validateOutputPath(p, fmt.Sprintf("narrate.commit.add[%d]", i)); len(pathErrs) > 0 {
			errs = append(errs, pathErrs...)
		}
	}
	// narrate.commit.builds land a command-build's tree at a repo path before commit.
	// Fail loudly (not silently) if a binding points at a missing or non-command build.
	commandBuildIDs := make(map[string]bool)
	for _, b := range cfg.Builds {
		if b.Kind == "command" {
			commandBuildIDs[b.ID] = true
		}
	}
	for i, bd := range cfg.Narrate.Commit.Builds {
		bp := fmt.Sprintf("narrate.commit.builds[%d]", i)
		switch {
		case bd.Build == "":
			errs = append(errs, fmt.Sprintf("%s: build is required (the kind: command build whose tree to land)", bp))
		case !buildIDs[bd.Build]:
			errs = append(errs, fmt.Sprintf("%s: unknown build %q (not in builds[])", bp, bd.Build))
		case !commandBuildIDs[bd.Build]:
			errs = append(errs, fmt.Sprintf("%s: build %q is not a kind: command build — only command builds produce a committable tree here", bp, bd.Build))
		}
		if bd.Destination == "" {
			errs = append(errs, fmt.Sprintf("%s: destination is required (the in-repo path to land the tree)", bp))
		} else if dErrs := validateOutputPath(bd.Destination, bp+".destination"); len(dErrs) > 0 {
			errs = append(errs, dErrs...)
		}
	}

	// ── Manifest ────────────────────────────────────────────────────

	if !validManifestModes[cfg.Manifest.Mode] {
		errs = append(errs, fmt.Sprintf("manifest.mode: unknown mode %q (supported: ephemeral, workspace, commit, publish)", cfg.Manifest.Mode))
	}
	if cfg.Manifest.OutputDir != "" {
		if pathErrs := validateOutputPath(cfg.Manifest.OutputDir, "manifest.output_dir"); len(pathErrs) > 0 {
			errs = append(errs, pathErrs...)
		}
	}

	// ── Security ─────────────────────────────────────────────────────────

	if cfg.Security.OutputDir != "" {
		if pathErrs := validateOutputPath(cfg.Security.OutputDir, "security.output"); len(pathErrs) > 0 {
			errs = append(errs, pathErrs...)
		}
	}

	// ── Release ──────────────────────────────────────────────────────────

	if cfg.Release.SecuritySummary != "" {
		if pathErrs := validateOutputPath(cfg.Release.SecuritySummary, "release.security_summary"); len(pathErrs) > 0 {
			errs = append(errs, pathErrs...)
		}
	}

	// ── Duration/Size unit validation ───────────────────────────────────
	// Reject invalid values at load time, not at consumption time.

	for _, dv := range []struct{ path, val string }{
		{"lint.cache.max_age", cfg.Lint.Cache.MaxAge},
		{"build_cache.local.retention.max_age", cfg.BuildCache.Local.Retention.MaxAge},
		{"build_cache.external.retention.stale_age", cfg.BuildCache.External.Retention.StaleAge},
		{"build_cache.cleanup.prune.images.dangling.older_than", cfg.BuildCache.Cleanup.Prune.Images.Dangling.OlderThan},
		{"build_cache.cleanup.prune.images.unreferenced.older_than", cfg.BuildCache.Cleanup.Prune.Images.Unreferenced.OlderThan},
		{"build_cache.cleanup.prune.build_cache.older_than", cfg.BuildCache.Cleanup.Prune.BuildCache.OlderThan},
		{"build_cache.cleanup.prune.containers.exited.older_than", cfg.BuildCache.Cleanup.Prune.Containers.Exited.OlderThan},
		{"security.cache.trivy.max_age", cfg.Security.Cache.Trivy.MaxAge},
		{"security.cache.grype.max_age", cfg.Security.Cache.Grype.MaxAge},
	} {
		if dv.val != "" {
			if _, err := ParseDuration(dv.val); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", dv.path, err))
			}
		}
	}

	for _, sv := range []struct{ path, val string }{
		{"lint.cache.max_size", cfg.Lint.Cache.MaxSize},
		{"build_cache.local.retention.max_size", cfg.BuildCache.Local.Retention.MaxSize},
		{"build_cache.cleanup.prune.build_cache.keep_storage", cfg.BuildCache.Cleanup.Prune.BuildCache.KeepStorage},
		{"security.cache.trivy.max_size", cfg.Security.Cache.Trivy.MaxSize},
		{"security.cache.grype.max_size", cfg.Security.Cache.Grype.MaxSize},
	} {
		if sv.val != "" {
			if _, err := ParseSize(sv.val); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", sv.path, err))
			}
		}
	}

	// ── Identity graph validation (forges + repos + registries) ─────────

	if len(cfg.Forges) > 0 || len(cfg.Repos) > 0 || len(cfg.Registries) > 0 {
		errs = append(errs, ValidateIdentityGraph(cfg.Forges, cfg.Repos, cfg.Registries)...)
		errs = append(errs, ValidateTargetRegistryRefs(cfg.Targets, cfg.Registries)...)
		errs = append(errs, ValidateTargetRepoRefs(cfg.Targets, cfg.Repos)...)
	}

	// ── Signing trust profiles (policy-level; the single validation layer) ──
	errs = append(errs, ValidateSigningProfiles(cfg.Signing)...)
	errs = append(errs, ValidateTargetSigningProfileRefs(cfg.Targets, cfg.Signing)...)
	errs = append(errs, ValidateSigningConfig(cfg.SigningSetup)...)

	// ── Unused matcher warning (high signal, low cost) ──────────────────
	//
	// Cross-check declared branch matchers against references. If a matcher
	// is defined but unused, it's almost always a typo or leftover cruft.
	// Warn, don't block — just diagnostic.
	if len(cfg.Matchers.Branches) > 0 {
		referenced := make(map[string]bool)
		for _, bb := range cfg.Versioning.BranchBuilds {
			if bb.Match != "" {
				referenced[bb.Match] = true
			}
		}
		for _, t := range cfg.Targets {
			for _, b := range t.When.Branches {
				if !strings.HasPrefix(b, "re:") && isIdentifier(b) {
					referenced[b] = true
				}
			}
		}
		for name := range cfg.Matchers.Branches {
			if !referenced[name] {
				warnings = append(warnings, fmt.Sprintf(
					"matcher %q is defined but not referenced by any branch_build or target.when.branches",
					name))
			}
		}
	}

	// ── Test suites ──────────────────────────────────────────────────────
	// Own schema (NOT lint's ModuleConfig). tool ∈ {go,rust,script}; script
	// requires command and go|rust forbid it; gate ∈ {"",perform,advisory}
	// (publish reserved for v2); ids unique.
	testIDs := make(map[string]bool, len(cfg.Test.Suites))
	for i, s := range cfg.Test.Suites {
		tpath := fmt.Sprintf("test.suites[%d]", i)
		if s.ID == "" {
			errs = append(errs, fmt.Sprintf("%s: id is required", tpath))
		} else if testIDs[s.ID] {
			errs = append(errs, fmt.Sprintf("%s: duplicate id %q", tpath, s.ID))
		}
		testIDs[s.ID] = true

		switch s.Tool {
		case TestToolGo, TestToolRust:
			if s.Command != "" {
				errs = append(errs, fmt.Sprintf("%s: tool %q does not take a command (use args for extra flags, or tool: script)", tpath, s.Tool))
			}
		case TestToolScript:
			if strings.TrimSpace(s.Command) == "" {
				errs = append(errs, fmt.Sprintf("%s: tool script requires a command", tpath))
			}
		case "":
			errs = append(errs, fmt.Sprintf("%s: tool is required (go, rust, or script)", tpath))
		default:
			errs = append(errs, fmt.Sprintf("%s: unknown tool %q (supported: go, rust, script)", tpath, s.Tool))
		}

		switch s.Gate {
		case "", GatePerform, GateAdvisory:
		case GatePublish:
			errs = append(errs, fmt.Sprintf("%s: gate %q is reserved and not yet implemented (use perform or advisory)", tpath, s.Gate))
		default:
			errs = append(errs, fmt.Sprintf("%s: unknown gate %q (supported: perform, advisory)", tpath, s.Gate))
		}

		if s.CoverageMin != nil {
			if s.Coverage == nil || !*s.Coverage {
				errs = append(errs, fmt.Sprintf("%s: coverage_min requires coverage: true", tpath))
			}
			if *s.CoverageMin <= 0 || *s.CoverageMin > 100 {
				errs = append(errs, fmt.Sprintf("%s: coverage_min must be in (0, 100], got %g", tpath, *s.CoverageMin))
			}
		}
	}

	// ── Toolchains ───────────────────────────────────────────────────────
	// Semantic validation of the parsed constraint model (grammar only). The config is
	// pure intent; the machine-maintained resolution + digest live in
	// .stagefreight/toolchains.lock, so there is no sha256/resolved to validate here.
	for name, c := range cfg.Toolchains.Desired {
		tpath := fmt.Sprintf("toolchains.desired.%s", name)
		constraint := strings.TrimSpace(c.Constraint)
		if constraint == "" {
			errs = append(errs, fmt.Sprintf("%s: version is required", tpath))
			continue
		}
		if verr := depversion.ValidateConstraint(constraint); verr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tpath, verr))
		}
	}

	if len(errs) > 0 {
		return warnings, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return warnings, nil
}


// cfPagesProjectRe matches Cloudflare Pages' project-name rule: lowercase letters,
// digits, and hyphens; 1–58 chars; no leading or trailing hyphen. A single char is
// allowed (the optional middle+tail group covers names of length 2+).
var cfPagesProjectRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,56}[a-z0-9])?$`)

// validateTarget checks kind-specific field constraints on a target.
func validateTarget(t TargetConfig, path string, buildIDs map[string]bool, matchers MatchersConfig, registries []RegistryConfig) []string {
	var errs []string

	switch t.Kind {
	case "registry":
		if t.Build == "" {
			errs = append(errs, fmt.Sprintf("%s: kind registry requires build reference", path))
		}
		// URL and path validation only for inline targets (no registry: ref).
		// When registry: is set, identity comes from registries[].
		if t.Registry == "" {
			if t.URL == "" {
				errs = append(errs, fmt.Sprintf("%s: kind registry requires registry: or url:", path))
			}
			if t.Path == "" {
				errs = append(errs, fmt.Sprintf("%s: kind registry requires path when using inline url", path))
			}
		}
		// Disallow release-only fields
		if len(t.Aliases) > 0 {
			errs = append(errs, fmt.Sprintf("%s: aliases is not valid for kind registry (use tags)", path))
		}
		if t.SyncRelease || t.SyncAssets {
			errs = append(errs, fmt.Sprintf("%s: sync_release/sync_assets are not valid for kind registry", path))
		}

	case "docker-readme":
		if t.Registry == "" {
			if t.URL == "" {
				errs = append(errs, fmt.Sprintf("%s: kind docker-readme requires registry: or url:", path))
			}
			if t.Path == "" {
				errs = append(errs, fmt.Sprintf("%s: kind docker-readme requires path when using inline url", path))
			}
		}
		if t.Build != "" {
			errs = append(errs, fmt.Sprintf("%s: kind docker-readme does not use build reference", path))
		}

	case "gitlab-component":
		if len(t.SpecFiles) == 0 {
			errs = append(errs, fmt.Sprintf("%s: kind gitlab-component requires spec_files", path))
		}
		if t.Build != "" {
			errs = append(errs, fmt.Sprintf("%s: kind gitlab-component does not use build reference", path))
		}

	case "release":
		// Mirror-referenced release: forge identity comes from repos.
		if t.Mirror != "" {
			// Note: mirror field references repos[].id, validated in identity graph check
			// Mirror-referenced targets must not restate forge fields.
			if t.Provider != "" || t.URL != "" || t.ProjectID != "" || t.Credentials != "" {
				errs = append(errs, fmt.Sprintf("%s: mirror-referenced release must not set provider/url/project_id/credentials (resolved from mirror)", path))
			}
		} else {
			// Primary vs remote mode validation (explicit forge fields).
			remoteFields := 0
			if t.Provider != "" {
				remoteFields++
			}
			if t.URL != "" {
				remoteFields++
			}
			if t.ProjectID != "" {
				remoteFields++
			}
			if t.Credentials != "" {
				remoteFields++
			}

			if remoteFields > 0 && remoteFields < 4 {
				errs = append(errs, fmt.Sprintf("%s: remote release requires all of provider, url, project_id, credentials (got %d of 4)", path, remoteFields))
			}

			isPrimary := remoteFields == 0
			if isPrimary {
				if t.SyncRelease {
					errs = append(errs, fmt.Sprintf("%s: sync_release is only valid for remote release targets", path))
				}
				if t.SyncAssets {
					errs = append(errs, fmt.Sprintf("%s: sync_assets is only valid for remote release targets", path))
				}
			}
		}

		if t.Build != "" {
			errs = append(errs, fmt.Sprintf("%s: kind release does not use build reference", path))
		}

	case "binary-archive":
		if t.Build == "" {
			errs = append(errs, fmt.Sprintf("%s: kind binary-archive requires build reference", path))
		}
		if !validArchiveFormats[t.Format] {
			errs = append(errs, fmt.Sprintf("%s: unknown archive format %q (supported: auto, tar.gz, zip)", path, t.Format))
		}
		// Disallow registry-only fields
		if t.URL != "" {
			errs = append(errs, fmt.Sprintf("%s: url is not valid for kind binary-archive", path))
		}
		if t.Path != "" {
			errs = append(errs, fmt.Sprintf("%s: path is not valid for kind binary-archive", path))
		}
		if len(t.Tags) > 0 {
			errs = append(errs, fmt.Sprintf("%s: tags is not valid for kind binary-archive (use name template)", path))
		}

	case "generic-package":
		// Forge identity comes from repo; archives supply the files.
		if t.Repo == "" {
			errs = append(errs, fmt.Sprintf("%s: kind generic-package requires repo", path))
		}
		if t.Archives == "" {
			errs = append(errs, fmt.Sprintf("%s: kind generic-package requires archives", path))
		}
		// Version is the immutable package version (mandatory). Every mutable alias
		// must have an immutable version behind it — alias-only publication loses
		// history and weakens traceability, so it is rejected.
		if t.Version == "" {
			errs = append(errs, fmt.Sprintf("%s: kind generic-package requires version (immutable version; alias-only publication is not allowed)", path))
		}
		// Reject fields that belong to other kinds or restate forge identity.
		if t.Build != "" {
			errs = append(errs, fmt.Sprintf("%s: build is not valid for kind generic-package", path))
		}
		if t.Mirror != "" {
			errs = append(errs, fmt.Sprintf("%s: mirror is not valid for kind generic-package (forge identity comes from repo)", path))
		}
		if t.Provider != "" || t.URL != "" || t.ProjectID != "" || t.Credentials != "" {
			errs = append(errs, fmt.Sprintf("%s: provider/url/project_id/credentials are not valid for kind generic-package (resolved from repo)", path))
		}
		if t.Tag != "" {
			errs = append(errs, fmt.Sprintf("%s: tag is not valid for kind generic-package (use version)", path))
		}
		if t.Path != "" {
			errs = append(errs, fmt.Sprintf("%s: path is not valid for kind generic-package", path))
		}
		if len(t.Tags) > 0 {
			errs = append(errs, fmt.Sprintf("%s: tags is not valid for kind generic-package (use version/aliases)", path))
		}

	case "pages":
		// Exactly one source: a build's output tree OR a repo directory. Empty is
		// treated as unset (so build: "" is rejected the same as missing).
		hasBuild := t.Build != ""
		hasDir := t.Dir != ""
		if hasBuild == hasDir {
			errs = append(errs, fmt.Sprintf("%s: kind pages requires exactly one of build or dir", path))
		}
		// Provider is required — Cloudflare and GitHub are co-equal, no default.
		switch t.Provider {
		case "cloudflare", "github":
		default:
			errs = append(errs, fmt.Sprintf("%s: kind pages requires provider: cloudflare or github (got %q)", path, t.Provider))
		}
		// Cloudflare project name (project:, else the target id) must satisfy CF's
		// naming rules — validated at load so it fails clearly here, not opaquely at
		// deploy/create time. GitHub has no such constraint (it deploys to the
		// gh-pages branch of an existing repo).
		if t.Provider == "cloudflare" {
			name := t.Project
			if name == "" {
				name = t.ID
			}
			if !cfPagesProjectRe.MatchString(name) {
				errs = append(errs, fmt.Sprintf("%s: cloudflare pages project name %q is invalid (lowercase letters, digits, and hyphens; 1–58 chars; no leading/trailing hyphen) — set project: if the target id can't satisfy this", path, name))
			}
		}
		// GitHub Pages serves a single custom domain per site (one CNAME record), so a
		// list is a configuration mistake, not a silent truncation. Cloudflare attaches
		// every listed domain. To serve the extras on GitHub Pages, redirect/proxy them
		// to the canonical one, or use provider: cloudflare for native multi-domain.
		if t.Provider == "github" && len(t.Domain) > 1 {
			errs = append(errs, fmt.Sprintf("%s: kind pages provider github supports a single custom domain, got %d (%s) — GitHub Pages writes one CNAME; redirect/proxy the others or use provider: cloudflare for native multi-domain", path, len(t.Domain), strings.Join(t.Domain, ", ")))
		}
		// Versioning: P1 implements only "replace"; "keep" is reserved (fail loudly
		// rather than silently ignore, so nobody assumes it works).
		if t.Versioning != nil {
			switch t.Versioning.Mode {
			case "", "replace":
			case "keep":
				errs = append(errs, fmt.Sprintf("%s: kind pages versioning mode \"keep\" is not yet implemented (only \"replace\")", path))
			default:
				errs = append(errs, fmt.Sprintf("%s: kind pages unknown versioning mode %q (supported: replace)", path, t.Versioning.Mode))
			}
		}
		// Reject fields that belong to other kinds.
		if t.URL != "" || t.Path != "" || t.Registry != "" || t.Repo != "" {
			errs = append(errs, fmt.Sprintf("%s: url/path/registry/repo are not valid for kind pages", path))
		}
		if len(t.Tags) > 0 || len(t.Aliases) > 0 {
			errs = append(errs, fmt.Sprintf("%s: tags/aliases are not valid for kind pages", path))
		}
	}

	return errs
}

// validateWhen checks the when block for valid pattern references and events.
func validateWhen(w TargetCondition, path string, versioning VersioningConfig, matchers MatchersConfig) []string {
	var errs []string

	// Build tag source id set for when.git_tags reference validation.
	tagSourceIDs := make(map[string]bool, len(versioning.TagSources))
	for _, ts := range versioning.TagSources {
		tagSourceIDs[ts.ID] = true
	}
	for _, entry := range w.GitTags {
		if strings.HasPrefix(entry, "re:") {
			continue // inline regex, skip pattern lookup
		}
		if !isIdentifier(entry) {
			continue // not a pattern name, will be treated as regex by match logic
		}
		if !tagSourceIDs[entry] {
			errs = append(errs, fmt.Sprintf("%s.when.git_tags: unknown tag source %q (not in versioning.tag_sources)", path, entry))
		}
	}

	for _, entry := range w.Branches {
		if strings.HasPrefix(entry, "re:") {
			continue
		}
		if !isIdentifier(entry) {
			continue
		}
		if _, ok := matchers.Branches[entry]; !ok {
			errs = append(errs, fmt.Sprintf("%s.when.branches: unknown matcher %q (not in matchers.branches)", path, entry))
		}
	}

	for _, event := range w.Events {
		if !validEvents[event] {
			events := make([]string, 0, len(validEvents))
			for e := range validEvents {
				events = append(events, e)
			}
			errs = append(errs, fmt.Sprintf("%s.when.events: unknown event %q (supported: %s)", path, event, strings.Join(events, ", ")))
		}
	}

	return errs
}

// validateNarratorItem checks kind, placement, and field constraints for a narrator item.
func validateNarratorItem(item NarratorItem, path string, buildIDs map[string]bool) []string {
	var errs []string

	// Kind validation
	if item.Kind == "" {
		errs = append(errs, fmt.Sprintf("%s: kind is required", path))
		return errs
	}
	if !validNarratorItemKinds[item.Kind] {
		kinds := make([]string, 0, len(validNarratorItemKinds))
		for k := range validNarratorItemKinds {
			kinds = append(kinds, k)
		}
		errs = append(errs, fmt.Sprintf("%s: unknown narrator item kind %q (supported: %s)", path, item.Kind, strings.Join(kinds, ", ")))
		return errs
	}

	// Placement validation (break kind doesn't need placement,
	// build-contents can use output_file instead — validated in kind-specific block)
	if item.Kind != "break" && item.Kind != "build-contents" {
		if !hasPlacementSelector(item.Placement) {
			errs = append(errs, fmt.Sprintf("%s: placement requires at least one selector (between, after, before, or heading)", path))
		}
	}

	// Placement mode validation
	if !validPlacementModes[item.Placement.Mode] {
		errs = append(errs, fmt.Sprintf("%s: unknown placement mode %q", path, item.Placement.Mode))
	}

	// Kind-specific validation
	switch item.Kind {
	case "badge":
		if item.Text == "" {
			errs = append(errs, fmt.Sprintf("%s: kind badge requires text (badge label)", path))
		}
		if item.Output != "" {
			if pathErrs := validateOutputPath(item.Output, path); len(pathErrs) > 0 {
				errs = append(errs, pathErrs...)
			}
		}

	case "shield":
		if item.Shield == "" {
			errs = append(errs, fmt.Sprintf("%s: kind shield requires shield (shields.io path)", path))
		}

	case "text":
		if item.Content == "" {
			errs = append(errs, fmt.Sprintf("%s: kind text requires content", path))
		}

	case "component":
		if item.Spec == "" {
			errs = append(errs, fmt.Sprintf("%s: kind component requires spec (component spec file path)", path))
		}

	case "include":
		if item.Path == "" {
			errs = append(errs, fmt.Sprintf("%s: kind include requires path (file path to include)", path))
		}

	case "props":
		if item.Type == "" {
			errs = append(errs, fmt.Sprintf("%s: kind props requires type (props resolver type ID)", path))
		}

	case "build-contents":
		if item.Section == "" {
			errs = append(errs, fmt.Sprintf("%s: kind build-contents requires section (dot-path into manifest)", path))
		}
		// Build ownership must be explicit, never inferred from build-list order.
		// An explicit source path (its own manifest) sidesteps build selection.
		if item.Build != "" {
			if !buildIDs[item.Build] {
				errs = append(errs, fmt.Sprintf("%s: build %q is not a configured build", path, item.Build))
			}
		} else if item.Source == "" && len(buildIDs) > 1 {
			errs = append(errs, fmt.Sprintf("%s: kind build-contents requires build (owning build id) when multiple builds are configured", path))
		}
		if item.Renderer == "" {
			errs = append(errs, fmt.Sprintf("%s: kind build-contents requires renderer (table, list, or kv)", path))
		} else if item.Renderer != "table" && item.Renderer != "list" && item.Renderer != "kv" && item.Renderer != "badges" && item.Renderer != "versions" {
			errs = append(errs, fmt.Sprintf("%s: unknown renderer %q (supported: table, list, kv, badges, versions)", path, item.Renderer))
		}
		if item.OutputFile != "" {
			if pathErrs := validateOutputPath(item.OutputFile, path+".output_file"); len(pathErrs) > 0 {
				errs = append(errs, pathErrs...)
			}
		}
		// Wrap validation
		if item.Wrap != "" && item.Wrap != "details" {
			errs = append(errs, fmt.Sprintf("%s: unknown wrap value %q (supported: details)", path, item.Wrap))
		}
		if item.Wrap == "details" && item.Summary == "" {
			errs = append(errs, fmt.Sprintf("%s: summary is required when wrap=details", path))
		}
		// build-contents can work with either placement (section embedding) or output_file, or both
		// but needs at least one destination
		if !hasPlacementSelector(item.Placement) && item.OutputFile == "" {
			errs = append(errs, fmt.Sprintf("%s: kind build-contents requires placement selector or output_file (at least one destination)", path))
		}
	}

	return errs
}

// hasPlacementSelector returns true if at least one placement selector is set.
func hasPlacementSelector(p NarratorPlacement) bool {
	return (p.Between != [2]string{}) || p.After != "" || p.Before != "" || p.Heading != ""
}

// validateOutputPath checks that an output path is safe.
func validateOutputPath(p string, itemPath string) []string {
	var errs []string

	if p == "" {
		errs = append(errs, fmt.Sprintf("%s: output path is empty", itemPath))
		return errs
	}

	// Absolute path
	if filepath.IsAbs(p) {
		errs = append(errs, fmt.Sprintf("%s: output path %q must be relative, not absolute", itemPath, p))
		return errs
	}

	// Tilde
	if strings.HasPrefix(p, "~") {
		errs = append(errs, fmt.Sprintf("%s: output path %q must not start with ~", itemPath, p))
		return errs
	}

	// Windows drive prefix
	if len(p) >= 2 && p[1] == ':' && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		errs = append(errs, fmt.Sprintf("%s: output path %q looks like a Windows drive path", itemPath, p))
		return errs
	}

	// Path traversal
	if strings.Contains(p, "..") {
		errs = append(errs, fmt.Sprintf("%s: output path %q must not contain '..'", itemPath, p))
		return errs
	}

	// Normalize: strip leading ./ then compare with filepath.Clean
	normalized := strings.TrimPrefix(p, "./")
	clean := filepath.Clean(normalized)
	if clean != normalized {
		errs = append(errs, fmt.Sprintf("%s: output path %q is not in canonical form (cleaned to %q)", itemPath, p, clean))
		return errs
	}

	return errs
}
