package config

import (
	"fmt"
	"os"
	"strings"
)

// CIEvent derives the current CI event in the canonical when.events vocabulary. Tag
// presence (CI_COMMIT_TAG / SF_CI_TAG) is the authoritative push-vs-tag signal:
// GitLab reports CI_PIPELINE_SOURCE=push even for tag pushes, and the dev channel
// synthesizes its tag locally (never exporting SF_CI_TAG), so a tag *string* is not
// a reliable event signal. Otherwise the raw forge source (SF_CI_EVENT) is mapped to
// the canonical vocabulary via NormalizeEvent, so gates authored once (events:[manual])
// match on every forge despite their differing raw names.
func CIEvent() string {
	if os.Getenv("SF_CI_TAG") != "" || os.Getenv("CI_COMMIT_TAG") != "" {
		return "tag"
	}
	if e := os.Getenv("SF_CI_EVENT"); e != "" {
		if n := NormalizeEvent(e); n != "" {
			return n
		}
	}
	return "push"
}

// NormalizeEvent maps a forge's raw pipeline-source string to the canonical
// when.events vocabulary (see validEvents), so a config gate is written once in
// portable terms and matches on every forge. The raw source is CI_PIPELINE_SOURCE
// on GitLab, github.event_name on GitHub/Gitea/Forgejo, and Build.Reason on Azure —
// and the single "manual re-run" concept has five different raw names across them
// (web/api/trigger, workflow_dispatch, Manual), which all collapse to "manual". An
// unrecognized source passes through lowercased (lenient: it still matches a
// literally-named gate), and empty stays empty (the unknown-event lenience in
// EventMatches then applies).
func NormalizeEvent(raw string) string {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "web", "api", "trigger", "manual", "workflow_dispatch":
		return "manual" // human/automation re-ran this ref's pipeline (not a code push)
	case "push", "individualci", "batchedci", "continuousintegration":
		return "push"
	case "schedule", "scheduled", "cron":
		return "schedule"
	case "merge_request":
		return "merge_request"
	case "pull_request", "pullrequest":
		return "pull_request"
	case "tag", "release":
		return v
	default:
		return v
	}
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

// CIProvider returns the current forge provider (github/gitlab/gitea/forgejo) from the
// CI environment ("" if none), for forge-scoped target eligibility.
func CIProvider() string {
	return os.Getenv("SF_CI_PROVIDER")
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
func TargetEligibility(t TargetConfig, event, branch, tag, forge string, tagPolicies, branchPolicies map[string]string) MatchResult {
	// No condition-sets → unconditional (eligible everywhere).
	if len(t.When) == 0 {
		return MatchResult{Eligible: true}
	}
	// A single condition-set preserves its precise rejection reason.
	if len(t.When) == 1 {
		return conditionEligibility(t.When[0], event, branch, tag, forge, tagPolicies, branchPolicies)
	}
	// OR over condition-sets: eligible if ANY matches.
	for _, c := range t.When {
		if r := conditionEligibility(c, event, branch, tag, forge, tagPolicies, branchPolicies); r.Eligible {
			return r
		}
	}
	return MatchResult{Reason: fmt.Sprintf("no when: condition matched (event=%q tag=%q branch=%q forge=%q)", event, tag, branch, forge)}
}

// conditionEligibility interprets ONE condition-set (events, then git_tags, then
// branches, then forges).
func conditionEligibility(c TargetCondition, event, branch, tag, forge string, tagPolicies, branchPolicies map[string]string) MatchResult {
	if !EventMatches(c.Events, event) {
		return MatchResult{Reason: fmt.Sprintf("run source %q not in events:%v", event, c.Events)}
	}
	if len(c.GitTags) > 0 && tag != "" {
		if !MatchPatternsWithPolicy(c.GitTags, tag, tagPolicies) {
			return MatchResult{Reason: fmt.Sprintf("tag %q not in git_tags:%v", tag, c.GitTags)}
		}
	}
	if len(c.Branches) > 0 {
		if !MatchPatternsWithPolicy(c.Branches, branch, branchPolicies) {
			return MatchResult{Reason: fmt.Sprintf("branch %q not in branches:%v", branch, c.Branches)}
		}
	}
	if len(c.Forges) > 0 && !forgeMatches(c.Forges, forge) {
		return MatchResult{Reason: fmt.Sprintf("forge %q not in forges:%v", forge, c.Forges)}
	}
	return MatchResult{Eligible: true}
}

// forgeMatches reports whether the current forge provider is in the allow-list
// (case-insensitive exact match on provider name).
func forgeMatches(forges []string, forge string) bool {
	for _, f := range forges {
		if strings.EqualFold(strings.TrimSpace(f), forge) {
			return true
		}
	}
	return false
}

// NotificationEligibility interprets a notification's when: — the SAME grammar as
// publish targets (events/branches/git_tags/forges, OR over condition-sets) plus
// the outcomes: dimension. Lives here, in the one when: interpreter, per the
// eligibility-routing invariant. outcome is the run's word: success | failure |
// warning (unknown matches only an empty outcomes:).
func NotificationEligibility(when WhenConditions, outcome, event, branch, tag, forge string, tagPolicies, branchPolicies map[string]string) MatchResult {
	if len(when) == 0 {
		return MatchResult{Eligible: true}
	}
	if len(when) == 1 {
		return notifyConditionEligibility(when[0], outcome, event, branch, tag, forge, tagPolicies, branchPolicies)
	}
	for _, c := range when {
		if r := notifyConditionEligibility(c, outcome, event, branch, tag, forge, tagPolicies, branchPolicies); r.Eligible {
			return r
		}
	}
	return MatchResult{Reason: fmt.Sprintf("no when: condition matched (outcome=%q event=%q tag=%q branch=%q)", outcome, event, tag, branch)}
}

func notifyConditionEligibility(c TargetCondition, outcome, event, branch, tag, forge string, tagPolicies, branchPolicies map[string]string) MatchResult {
	if len(c.Outcomes) > 0 && !containsFold(c.Outcomes, outcome) {
		return MatchResult{Reason: fmt.Sprintf("run outcome %q not in outcomes:%v", outcome, c.Outcomes)}
	}
	return conditionEligibility(c, event, branch, tag, forge, tagPolicies, branchPolicies)
}

func containsFold(list []string, v string) bool {
	for _, e := range list {
		if strings.EqualFold(strings.TrimSpace(e), v) {
			return true
		}
	}
	return false
}

// TargetMatches reports whether a target's when: conditions are satisfied — the
// bool view of TargetEligibility for call sites that don't narrate. See the
// TargetEligibility invariant: this is the single shared gating predicate;
// capabilities must not interpret when: themselves.
func TargetMatches(t TargetConfig, event, branch, tag, forge string, tagPolicies, branchPolicies map[string]string) bool {
	return TargetEligibility(t, event, branch, tag, forge, tagPolicies, branchPolicies).Eligible
}

// TargetMatchesEnv evaluates TargetMatches against the current CI environment,
// resolving policy maps from cfg. Convenience for contributors that gate on when:.
func TargetMatchesEnv(t TargetConfig, cfg *Config) bool {
	tagPolicies := make(map[string]string, len(cfg.Git.Tags))
	for _, ts := range cfg.Git.Tags {
		tagPolicies[ts.ID] = ts.Pattern
	}
	return TargetMatches(t, CIEvent(), CIBranch(), CITag(), CIProvider(), tagPolicies, cfg.Git.Branches)
}

// TargetIsUnconditional reports whether a target declares no when: constraints at
// all (no events, branches, or git_tags) — i.e. it is eligible in every CI
// context. It exists so callers can ask this WITHOUT reading t.When.* directly:
// an emptiness probe is still an interpretation of target constraints, and per
// the eligibility-routing invariant there is exactly one interpreter (this package).
func TargetIsUnconditional(t TargetConfig) bool {
	for _, c := range t.When {
		if len(c.Events) > 0 || len(c.Branches) > 0 || len(c.GitTags) > 0 || len(c.Forges) > 0 {
			return false
		}
	}
	return true
}
