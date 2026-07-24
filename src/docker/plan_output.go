package docker

import (
	"encoding/json"
	"time"

	"github.com/PrPlanIT/StageFreight/src/runtime"
)

// PlanOutput is the stable external schema for Docker lifecycle plan data.
// Designed for DD-UI consumption, CI artifacts, and --output json.
// Backend decides; frontend renders. No frontend guesswork.
type PlanOutput struct {
	Mode      string       `json:"mode"`
	Backend   string       `json:"backend"`
	Generated time.Time    `json:"generated_at"`
	Summary   PlanSummary  `json:"summary"`
	Trust     TrustOutput  `json:"trust"`
	Targets   []TargetPlan `json:"targets"`
}

// PlanSummary provides top-level counts.
type PlanSummary struct {
	Total   int            `json:"total"`
	Drifted int            `json:"drifted"`
	Orphans int            `json:"orphans"`
	Blocked int            `json:"blocked"`
	Actions map[string]int `json:"actions"`
}

// TrustOutput is the external representation of DiscoveryTrust.
type TrustOutput struct {
	Level   string      `json:"level"`
	Checks  TrustChecks `json:"checks"`
	Reasons []string    `json:"reasons,omitempty"`
}

// TrustChecks shows individual trust gate results.
type TrustChecks struct {
	Sentinel        bool `json:"sentinel"`
	IaCRoot         bool `json:"iac_root"`
	Scan            bool `json:"scan"`
	RepoIdentity    bool `json:"repo_identity"`
	DeclaredTargets bool `json:"declared_targets"`
}

// TargetPlan groups stacks and orphans per host.
type TargetPlan struct {
	Name      string         `json:"name"`
	Transport string         `json:"transport"`
	Stacks    []StackPlan    `json:"stacks"`
	Orphans   []OrphanPlan   `json:"orphans,omitempty"`
	Anomalies []AnomalyEntry `json:"anomalies,omitempty"`
}

// StackPlan is the resolved state of a declared stack.
type StackPlan struct {
	Name    string       `json:"name"`
	Project string       `json:"project"`
	Scope   string       `json:"scope"`
	Status  string       `json:"status"` // in_sync | drift | blocked | unknown
	Drift   DriftDetail  `json:"drift"`
	Action  ActionDetail `json:"action"`
}

// DriftDetail provides structured drift information.
type DriftDetail struct {
	Detected bool   `json:"detected"`
	Kind     string `json:"kind"` // tier1 | tier2 | none | unknown
	Reason   string `json:"reason"`
}

// ActionDetail shows requested vs effective action.
type ActionDetail struct {
	Requested     string `json:"requested"`
	Effective     string `json:"effective"`
	Allowed       bool   `json:"allowed"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

// OrphanPlan represents a running project with no IaC declaration.
type OrphanPlan struct {
	Project string       `json:"project"`
	Scope   string       `json:"scope"`
	Reason  string       `json:"reason"`
	Action  ActionDetail `json:"action"`
}

// AnomalyEntry represents a detected anomaly on a target.
type AnomalyEntry struct {
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// BuildPlanOutput converts internal plan + trust into the stable external schema.
// Uses DockerPlanMeta signals directly — no inference from action type.
func BuildPlanOutput(plan *runtime.LifecyclePlan, trust DiscoveryTrust, targets []HostTarget) PlanOutput {
	output := PlanOutput{
		Mode:      plan.Mode,
		Backend:   plan.Backend,
		Generated: time.Now(),
		Summary:   PlanSummary{Actions: map[string]int{}},
		Trust: TrustOutput{
			Level: string(trust.Level),
			Checks: TrustChecks{
				Sentinel:        trust.Sentinel,
				IaCRoot:         trust.IaCRootExists,
				Scan:            trust.ScanSucceeded,
				RepoIdentity:    trust.RepoIdentityMatch,
				DeclaredTargets: trust.DeclaredTargets,
			},
		},
	}

	for _, r := range trust.Reasons {
		output.Trust.Reasons = append(output.Trust.Reasons, string(r))
	}

	// Build target map.
	targetMap := map[string]*TargetPlan{}
	var targetOrder []string
	for _, t := range targets {
		transport := "ssh"
		if t.Vars["docker_local"] == "true" {
			transport = "local"
		}
		tp := &TargetPlan{Name: t.Name, Transport: transport}
		targetMap[t.Name] = tp
		targetOrder = append(targetOrder, t.Name)
	}

	ensureTarget := func(name string) *TargetPlan {
		tp, ok := targetMap[name]
		if !ok {
			tp = &TargetPlan{Name: name, Transport: "unknown"}
			targetMap[name] = tp
			targetOrder = append(targetOrder, name)
		}
		return tp
	}

	// Process actions using real metadata signals.
	for _, pa := range plan.Actions {
		meta := ParseDockerPlanMeta(pa.Metadata)
		tp := ensureTarget(meta.Scope)
		output.Summary.Total++
		output.Summary.Actions[pa.Action]++

		if meta.IsOrphan {
			// Orphan — always counted regardless of action.
			output.Summary.Orphans++

			allowed := meta.BlockedReason == ""
			if !allowed {
				output.Summary.Blocked++
			}

			tp.Orphans = append(tp.Orphans, OrphanPlan{
				Project: meta.Stack,
				Scope:   meta.Scope,
				Reason:  meta.DriftReason,
				Action: ActionDetail{
					Requested:     meta.RequestedAction,
					Effective:     pa.Action,
					Allowed:       allowed,
					BlockedReason: meta.BlockedReason,
				},
			})
			continue
		}

		// Regular stack — use DriftDetected signal, not action inference.
		if meta.DriftDetected {
			output.Summary.Drifted++
		}

		status := "in_sync"
		driftKind := "none"

		// Unknown: drift could not be evaluated (transport failure, etc.)
		if !meta.DriftDetected && meta.DriftReason != "" && meta.DriftTier == 2 &&
			meta.DriftReason != "no drift detected" {
			status = "unknown"
			driftKind = "unknown"
		} else if meta.DriftDetected {
			if meta.BlockedReason != "" {
				status = "blocked"
				output.Summary.Blocked++
			} else {
				status = "drift"
			}
			switch meta.DriftTier {
			case 1:
				driftKind = "tier1"
			case 2:
				driftKind = "tier2"
			default:
				driftKind = "unknown"
			}
		}

		allowed := meta.BlockedReason == ""

		tp.Stacks = append(tp.Stacks, StackPlan{
			Name:    meta.Stack,
			Project: meta.Stack,
			Scope:   meta.Scope,
			Status:  status,
			Drift: DriftDetail{
				Detected: meta.DriftDetected,
				Kind:     driftKind,
				Reason:   meta.DriftReason,
			},
			Action: ActionDetail{
				Requested:     meta.RequestedAction,
				Effective:     pa.Action,
				Allowed:       allowed,
				BlockedReason: meta.BlockedReason,
			},
		})
	}

	// Assemble targets in order.
	for _, name := range targetOrder {
		if tp, ok := targetMap[name]; ok {
			output.Targets = append(output.Targets, *tp)
		}
	}

	return output
}

// JSON produces the stable JSON output.
func (p PlanOutput) JSON() ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}
