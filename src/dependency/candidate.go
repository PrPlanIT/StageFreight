package dependency

import (
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	depversion "github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// PolicyLayer names a policy constraint that can exclude a version from a
// CandidateSet. When Admits rejects a version it returns the BINDING layer — the one
// that actually removed it — so callers explain the decision precisely rather than
// re-deriving it. (Lives in package dependency, not a sub-package, so it can share the
// SkipCategory / UpdateConfig types without an import cycle.)
type PolicyLayer string

const (
	PolicyNone             PolicyLayer = ""
	PolicyPinned           PolicyLayer = "pinned"            // toolchain governs the version (go replace)
	PolicyNativeConstraint PolicyLayer = "native_constraint" // manifest operator (cargo =/~/^/range)
	PolicyCeiling          PolicyLayer = "ceiling"           // max_update
	PolicyCooldown         PolicyLayer = "cooldown"          // min_release_age (data-dependent)
	PolicyPrerelease       PolicyLayer = "prerelease"        // stable-only unless current is prerelease
)

// CandidateSet is the per-dependency policy evaluation: whether the dep is an update
// candidate at all (Eligible), the selected Target, and a PREDICATE (Admits) that
// tests ANY version against the same declared policy. It is the single artifact the
// updater, remediation, and freshness reporting all read — one evaluation, many
// consumers, no re-derivation. Admits is a predicate rather than a materialized set so
// it works WITHOUT the full version list (which most ecosystems don't provide) and so
// the exclusion provenance (the binding PolicyLayer) is automatic. The set embeds the
// policy it was built under, so Admits is self-contained.
type CandidateSet struct {
	Dep            supplychain.Dependency
	Eligible       bool         // is this dep an update candidate?
	Category       SkipCategory // classification when not eligible (dep-level skip)
	Reason         string       // human reason, byte-identical to the legacy skipReason
	Target         string       // selected update target ("" when none / skipped)
	ResolvedTarget string       // set when a ceiling re-target lowered the target

	cfg UpdateConfig // the policy this set was constructed under (consumed by Admits)
}

// Construct evaluates declared policy for one dependency: it reproduces the legacy
// skip decision EXACTLY (same category + reason, same precedence) and computes the
// selected target. The filter is now a thin consumer of this — one evaluation the
// updater (here), remediation, and freshness all read.
func Construct(dep supplychain.Dependency, cfg UpdateConfig, ecosystemFilter map[string]bool, trackedFiles map[string]bool) CandidateSet {
	cs := CandidateSet{Dep: dep, cfg: cfg}
	if cat, reason := skipReason(dep, cfg, ecosystemFilter, trackedFiles); cat != SkipNone {
		cs.Category = cat
		cs.Reason = reason
		return cs // not eligible (Eligible is the zero value)
	}
	cs.Eligible = true
	cs.Target = dep.UpdateTarget()
	// A non-vulnerable candidate whose natural target exceeds the ceiling is kept only
	// because an in-ceiling re-target exists — record it so the target is the lower version.
	if len(dep.Vulnerabilities) == 0 {
		if t := ceilingRetarget(dep, cfg.MaxUpdate); t != "" {
			cs.ResolvedTarget = t
			cs.Target = t
		}
	}
	return cs
}

// Admits reports whether version v is acceptable under this dependency's declared
// policy and, when not, the BINDING policy layer plus a human detail. Layers are
// tested most-fundamental first, so the first rejection is the tightest bound. This is
// what RemediationEvaluation will ask of an advisory's fixed-in ("is the fix reachable
// under policy?"). Note: cooldown (min_release_age) is data-dependent — evaluable only
// when per-version publish dates are available, which most ecosystems don't yet supply,
// so it is not enforced here beyond its layer being defined for downstream use.
func (cs CandidateSet) Admits(v string) (bool, PolicyLayer, string) {
	dep := cs.Dep
	// The toolchain already selected this dep's version (go replace) — nothing admitted.
	if dep.Pinned != "" {
		return false, PolicyPinned, dep.Pinned
	}
	// Native manifest constraint (cargo operator) — v must satisfy it.
	if dep.Constraint != "" && !depversion.Satisfies(dep.Constraint, v) {
		return false, PolicyNativeConstraint, dep.Constraint
	}
	// max_update ceiling — v's update-type from current must be within it. A vulnerable
	// dep is exempt (the remediation floor overrides the ceiling), matching skipReason.
	if len(dep.Vulnerabilities) == 0 && updateTypeExceedsCeiling(updateType(dep.Current, v), cs.cfg.MaxUpdate) {
		label := cs.cfg.MaxUpdate
		if label == "" {
			label = "major"
		}
		return false, PolicyCeiling, label
	}
	// Stable-only: reject a prerelease unless the current pin is itself a prerelease.
	if depversion.IsPrerelease(v) && !depversion.IsPrerelease(dep.Current) {
		return false, PolicyPrerelease, v
	}
	return true, PolicyNone, ""
}
