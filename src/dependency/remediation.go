package dependency

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/vulnerability/severity"
)

// RemediationState is the verdict for one advisory on one dependency: was it (or can
// it be) fixed, and if not, why. Severity comes from the advisory; STATE from this
// evaluation; the two are orthogonal — a CRITICAL stays CRITICAL whether remediated
// or blocked.
type RemediationState string

const (
	StateRemediated         RemediationState = "remediated"          // an applied update carried the fix
	StateReachableUnapplied RemediationState = "reachable_unapplied" // fix reachable under policy but not applied (evaluate-only / apply gap)
	StateBlockedByPolicy    RemediationState = "blocked_by_policy"   // fix exists but is excluded by declared policy — UNREMEDIABLE UNDER POLICY
	StateBlockedByCooldown  RemediationState = "blocked_by_cooldown" // fix exists but held by min_release_age (reserved: cooldown data-dependent)
	StateNoFix              RemediationState = "no_fix"              // no fixed-in version published upstream
)

// RemediationEvaluation is the per-advisory verdict, derived by asking the
// dependency's CandidateSet whether the advisory's fixed-in version is reachable
// under declared policy. This is the first-class "can policy fix it, and if not why"
// artifact — the answer StageFreight needs before it can automerge anything.
type RemediationEvaluation struct {
	VulnID    string
	Package   string
	Version   string // the dep's current resolved version
	Severity  string // from the advisory, unchanged
	FixedIn   string
	State     RemediationState
	BlockedBy PolicyLayer // the binding layer when blocked
	Detail    string      // the binding constraint detail (e.g. "=1.8.0", "replace directive")
}

// EvaluateRemediation produces a verdict for every advisory across `deps`, using the
// CandidateSet predicate to test each advisory's fixed-in against declared policy.
// `result` (if non-nil) marks advisories an applied update actually fixed.
func EvaluateRemediation(deps []supplychain.Dependency, cfg UpdateConfig, result *UpdateResult) []RemediationEvaluation {
	fixed := map[string]bool{}
	if result != nil {
		for _, a := range result.Applied {
			for _, id := range a.CVEsFixed {
				fixed[id] = true
			}
		}
	}

	var out []RemediationEvaluation
	for _, d := range deps {
		if len(d.Vulnerabilities) == 0 {
			continue
		}
		cs := CandidateSet{Dep: d, cfg: cfg} // just enough for Admits
		for _, v := range d.Vulnerabilities {
			if v.ID == "" {
				continue
			}
			e := RemediationEvaluation{VulnID: v.ID, Package: d.Name, Version: d.Current, Severity: v.Severity, FixedIn: v.FixedIn}
			switch {
			case fixed[v.ID]:
				e.State = StateRemediated
			case strings.TrimSpace(v.FixedIn) == "":
				e.State = StateNoFix
			default:
				ok, layer, detail := cs.Admits(v.FixedIn)
				switch {
				case ok:
					e.State = StateReachableUnapplied
				case layer == PolicyCooldown:
					e.State, e.BlockedBy, e.Detail = StateBlockedByCooldown, layer, detail
				default:
					e.State, e.BlockedBy, e.Detail = StateBlockedByPolicy, layer, detail
				}
			}
			out = append(out, e)
		}
	}
	return out
}

// Residuals filters evaluations to the unremediated advisories at or above the
// failOn threshold — the gating set. Byte-identical to the prior residual gate
// (state != remediated ⟺ advisory not in the applied fixed-set), now carrying WHY.
// failOn "off"/"" or unrecognized → no gate (nil).
func Residuals(evals []RemediationEvaluation, failOn string) []RemediationEvaluation {
	minRank := severity.Rank(severity.Normalize(failOn))
	if minRank == 0 {
		return nil
	}
	var out []RemediationEvaluation
	for _, e := range evals {
		if e.State == StateRemediated {
			continue
		}
		if severity.Rank(severity.Normalize(e.Severity)) >= minRank {
			out = append(out, e)
		}
	}
	return out
}

// Explain renders a residual's reason for disclosure. blocked_by_policy is surfaced
// as the first-class "unremediable under declared policy" outcome.
func (e RemediationEvaluation) Explain() string {
	switch e.State {
	case StateNoFix:
		return "no fix available upstream"
	case StateReachableUnapplied:
		return "fix " + e.FixedIn + " reachable but not applied (enable remediate)"
	case StateBlockedByCooldown:
		return "fix " + e.FixedIn + " held by cooldown"
	case StateBlockedByPolicy:
		return "unremediable under declared policy — fix " + e.FixedIn + " excluded by " + string(e.BlockedBy) + " (" + e.Detail + ")"
	default:
		return string(e.State)
	}
}
