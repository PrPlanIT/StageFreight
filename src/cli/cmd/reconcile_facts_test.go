package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runtime"
)

// TestReconcileFacts covers the {reconcile.*} vocabulary: counts, the declined
// fact only when >0 (so its line elides at zero), the backend's unit noun as
// data, per-action failure rows, and the derived outcome/reason.
func TestReconcileFacts(t *testing.T) {
	appCfg := &config.Config{}
	appCfg.GitOps.Cluster.Name = "dungeon"

	plan := &runtime.LifecyclePlan{
		Backend:  "flux",
		Actions:  []runtime.PlannedAction{{Name: "flux-system/infrastructure"}, {Name: "flux-system/apps"}, {Name: "compass/echoip"}},
		Declined: []runtime.PlannedAction{{Name: "temple-of-time/broken"}},
	}
	result := &runtime.LifecycleResult{Actions: []runtime.ActionResult{
		{Name: "flux-system/infrastructure", Success: true},
		{Name: "flux-system/apps", Success: true},
		{Name: "compass/echoip", Success: false, Message: "health check timeout"},
	}}

	facts, outcome, reason := reconcileFacts(appCfg, plan, result)
	want := map[string]string{
		"total": "3", "succeeded": "2", "failed": "1",
		"backend": "flux", "units": "kustomizations", "cluster": "dungeon",
		"declined": "1",
		"failures": "compass/echoip — health check timeout",
	}
	for k, v := range want {
		if facts[k] != v {
			t.Errorf("facts[%q] = %q, want %q", k, facts[k], v)
		}
	}
	if outcome != "failed" || reason != "1 of 3 kustomizations failed to reconcile" {
		t.Errorf("outcome/reason = %q / %q", outcome, reason)
	}

	// Clean run: no declined, no failures keys (their lines elide); success.
	clean := &runtime.LifecyclePlan{Backend: "flux", Actions: plan.Actions}
	okRes := &runtime.LifecycleResult{Actions: []runtime.ActionResult{
		{Success: true}, {Success: true}, {Success: true},
	}}
	facts, outcome, _ = reconcileFacts(appCfg, clean, okRes)
	if outcome != "success" || facts["succeeded"] != "3" {
		t.Errorf("clean run: outcome %q, succeeded %q", outcome, facts["succeeded"])
	}
	if _, ok := facts["declined"]; ok {
		t.Error("declined must be absent at zero (the line elides)")
	}
	if _, ok := facts["failures"]; ok {
		t.Error("failures must be absent on a clean run")
	}

	// Nil result (nothing executed): plan size with zero outcomes, still success.
	facts, outcome, _ = reconcileFacts(appCfg, clean, nil)
	if outcome != "success" || facts["total"] != "3" || facts["succeeded"] != "0" {
		t.Errorf("nil result: outcome %q facts %v", outcome, facts)
	}
}
