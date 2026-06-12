package config

import (
	"fmt"
	"os"
)

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

// MatchResult is the outcome of a target eligibility check: whether the target
// is eligible for the current CI context and, when it is not, a human-readable
// reason coupled to the decision. Narration reads Reason directly, so the
// explanation can never drift from the matcher logic that produced it.
type MatchResult struct {
	Eligible bool
	Reason   string // why NOT eligible; empty when Eligible
}

// TargetEligibility is the single authoritative interpretation of a target's
// when: conditions (events, then git_tags, then branches), returning the
// decision and — on rejection — the coupled reason.
//
// INVARIANT: every capability (docker, binary archives, release, retention,
// sync, and every future distribution capability) MUST route its when:
// interpretation through TargetEligibility/TargetMatches. Capability code must
// NOT inspect t.When.Events / t.When.Branches / t.When.GitTags directly. A new
// eligibility dimension is added here, to the framework — never bolted onto a
// caller. (This is the engine that replaced the old per-capability gates.)
//
// tagPolicies/branchPolicies resolve named patterns (versioning.tag_sources,
// matchers.branches); inline "re:" and "!" negation are handled by
// ResolvePatterns. Empty conditions never restrict.
func TargetEligibility(t TargetConfig, event, branch, tag string, tagPolicies, branchPolicies map[string]string) MatchResult {
	if !EventMatches(t.When.Events, event) {
		return MatchResult{Reason: fmt.Sprintf("run source %q not in events:%v", event, t.When.Events)}
	}
	if len(t.When.GitTags) > 0 && tag != "" {
		if !MatchPatternsWithPolicy(t.When.GitTags, tag, tagPolicies) {
			return MatchResult{Reason: fmt.Sprintf("tag %q not in git_tags:%v", tag, t.When.GitTags)}
		}
	}
	if len(t.When.Branches) > 0 {
		if !MatchPatternsWithPolicy(t.When.Branches, branch, branchPolicies) {
			return MatchResult{Reason: fmt.Sprintf("branch %q not in branches:%v", branch, t.When.Branches)}
		}
	}
	return MatchResult{Eligible: true}
}

// TargetMatches reports whether a target's when: conditions are satisfied — the
// bool view of TargetEligibility for call sites that don't narrate. See the
// TargetEligibility invariant: this is the single shared gating predicate;
// capabilities must not interpret when: themselves.
func TargetMatches(t TargetConfig, event, branch, tag string, tagPolicies, branchPolicies map[string]string) bool {
	return TargetEligibility(t, event, branch, tag, tagPolicies, branchPolicies).Eligible
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
