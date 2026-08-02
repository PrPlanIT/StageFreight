package build

import (
	"fmt"
	"regexp"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// VersionInfo is an alias for backward compatibility.
type VersionInfo = gitver.VersionInfo

// DetectVersion resolves version info using versioning config from the given Config.
// Config is REQUIRED — nil is rejected. Every caller must thread a valid
// *config.Config through to version detection. There is no legacy
// "just guess from git" path.
func DetectVersion(rootDir string, cfg *config.Config) (*gitver.VersionInfo, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required for version detection (nil is forbidden)")
	}
	opts, err := versioningOptsFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	return gitver.DetectVersionWithOpts(rootDir, opts)
}

// HasVersionScheme reports whether the config declares a version derivation
// scheme (git.versioning.branch_builds). Absence is a VALID state — an
// unversioned repo (a gitops config repo, say) has commits and tags but no
// version to derive — not a configuration error.
func HasVersionScheme(cfg *config.Config) bool {
	return cfg != nil && len(cfg.Git.Versioning.BranchBuilds) > 0
}

// DetectVersionLenient is DetectVersion for AUDIENCE-TEXT rendering (scribe
// bodies, narrate cards, notifications): an unversioned repo yields a minimal
// run identity (SHA/branch) with no error, so the template leaf-pass still runs
// and {version} simply elides — no "version detection failed" noise for a repo
// that never claimed to have a version. Build/publish/release paths keep the
// strict DetectVersion: THEY require a scheme, and its absence stays loud.
func DetectVersionLenient(rootDir string, cfg *config.Config) (*gitver.VersionInfo, error) {
	if !HasVersionScheme(cfg) {
		return gitver.IdentityOnly(rootDir), nil
	}
	return DetectVersion(rootDir, cfg)
}

// versioningOptsFromConfig builds gitver.VersioningOpts from a config.Config.
//
// INVARIANT:
// Matchers are declarative pattern definitions only — they do not carry
// behavior. BUT they DO participate in versioning's execution graph: branch
// rules reference matcher ids to decide which rule applies to the current
// branch. This reference is part of control flow, not just config lookup.
// Any future refactor that "decouples" matchers from versioning rule
// selection will break the branch_builds contract.
func versioningOptsFromConfig(cfg *config.Config) (*gitver.VersioningOpts, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required for version detection")
	}

	opts := &gitver.VersioningOpts{
		NoLineageMode:    cfg.Git.Versioning.NoLineage.Mode,
		NoLineageVersion: cfg.Git.Versioning.NoLineage.Version,
	}

	for _, ts := range cfg.Git.Tags {
		opts.TagSources = append(opts.TagSources, gitver.TagSource{
			ID:      ts.ID,
			Pattern: ts.Pattern,
		})
	}

	for _, bb := range cfg.Git.Versioning.BranchBuilds {
		rule := gitver.BranchRule{
			ID:          bb.ID,
			IsDefault:   bb.ID == "default",
			BaseFromIDs: append([]string(nil), bb.BaseFrom...), // defensive copy
			Format:      bb.Format,
		}
		if !rule.IsDefault {
			// Fail closed: do not silently accept an empty regex if the
			// matcher reference is unknown. Validation should already have
			// caught this, but double-enforce here — defense in depth.
			pattern, ok := cfg.Git.Branches[bb.Match]
			if !ok {
				return nil, fmt.Errorf(
					"versioning: branch_build %q references unknown matcher %q",
					bb.ID, bb.Match)
			}
			// Compile branch regex once at opts-build time — symmetry with
			// tag_sources compilation. No per-call regex.Compile in the hot path.
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf(
					"versioning: branch_build %q matcher %q regex invalid: %w",
					bb.ID, bb.Match, err)
			}
			rule.Match = re
		}
		opts.BranchRules = append(opts.BranchRules, rule)
	}

	return opts, nil
}
