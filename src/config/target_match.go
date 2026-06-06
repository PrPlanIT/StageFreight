package config

import "os"

// CIEvent derives the current CI event (push/tag/…) for when-matching. Tag
// presence (CI_COMMIT_TAG / SF_CI_TAG) is the authoritative push-vs-tag signal:
// GitLab reports CI_PIPELINE_SOURCE=push even for tag pushes, and the dev channel
// synthesizes its tag locally (never exporting SF_CI_TAG), so a tag *string* is
// not a reliable event signal. Explicit non-push/tag events are honored verbatim.
func CIEvent() string {
	if os.Getenv("SF_CI_TAG") != "" || os.Getenv("CI_COMMIT_TAG") != "" {
		return "tag"
	}
	if e := os.Getenv("SF_CI_EVENT"); e != "" && e != "push" && e != "tag" {
		return e
	}
	return "push"
}

// CIBranch returns the current branch from the CI environment ("" if none).
func CIBranch() string {
	if b := os.Getenv("CI_COMMIT_BRANCH"); b != "" {
		return b
	}
	if b := os.Getenv("GITHUB_REF_NAME"); b != "" {
		return b
	}
	return ""
}

// CITag returns the current tag from the CI environment ("" if none).
func CITag() string {
	if t := os.Getenv("SF_CI_TAG"); t != "" {
		return t
	}
	return os.Getenv("CI_COMMIT_TAG")
}

// TargetMatches reports whether a target's when: conditions are satisfied by the
// given CI context. It is the single shared gating predicate used by both the
// release runner and the build contributors, so event/branch/tag routing is
// identical everywhere. tagPolicies/branchPolicies resolve named patterns
// (versioning.tag_sources, matchers.branches); inline "re:" and "!" negation are
// handled by ResolvePatterns. Empty conditions never restrict.
func TargetMatches(t TargetConfig, event, branch, tag string, tagPolicies, branchPolicies map[string]string) bool {
	if !EventMatches(t.When.Events, event) {
		return false
	}
	if len(t.When.GitTags) > 0 && tag != "" {
		if !MatchPatternsWithPolicy(t.When.GitTags, tag, tagPolicies) {
			return false
		}
	}
	if len(t.When.Branches) > 0 {
		if !MatchPatternsWithPolicy(t.When.Branches, branch, branchPolicies) {
			return false
		}
	}
	return true
}

// TargetMatchesEnv evaluates TargetMatches against the current CI environment,
// resolving policy maps from cfg. Convenience for contributors that gate on when:.
func TargetMatchesEnv(t TargetConfig, cfg *Config) bool {
	tagPolicies := make(map[string]string, len(cfg.Versioning.TagSources))
	for _, ts := range cfg.Versioning.TagSources {
		tagPolicies[ts.ID] = ts.Pattern
	}
	return TargetMatches(t, CIEvent(), CIBranch(), CITag(), tagPolicies, cfg.Matchers.Branches)
}
