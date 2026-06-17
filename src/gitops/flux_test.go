package gitops

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/auditionproof"
)

func TestReconcileVerdicts_Classifies(t *testing.T) {
	dir := t.TempDir()
	if err := auditionproof.Write(dir, &auditionproof.Results{FluxValidate: &auditionproof.FluxValidate{
		Verdicts: map[string]auditionproof.Verdict{
			"flux-system/a": {Status: "pass"},
			"flux-system/b": {Status: "fail", Findings: []auditionproof.Finding{{Severity: "fail", Source: "core-schema", Message: "HelmRelease/x: schema error"}}},
			"flux-system/c": {Status: "warn"},
		},
	}}); err != nil {
		t.Fatal(err)
	}

	validated, failReasons, unavailable := reconcileVerdicts(dir)
	if unavailable != "" {
		t.Fatalf("unexpected unavailable: %q", unavailable)
	}
	if !validated["flux-system/a"] || !validated["flux-system/c"] {
		t.Errorf("pass/warn (a,c) should be accelerable: %v", validated)
	}
	if validated["flux-system/b"] {
		t.Errorf("fail 'b' must not be accelerable")
	}
	if failReasons["flux-system/b"] == "" {
		t.Errorf("b should carry a fail reason")
	}
}

// TestReconcileVerdicts_NoArtifact_FailClosed: a missing proof-results artifact
// must report unavailable (so the caller declines everything), NOT empty maps
// (which would silently accelerate unvalidated state).
func TestReconcileVerdicts_NoArtifact_FailClosed(t *testing.T) {
	_, _, unavailable := reconcileVerdicts(t.TempDir())
	if unavailable == "" {
		t.Fatal("missing artifact must report unavailable (fail-closed)")
	}
}

// TestReconcileVerdicts_Skipped_FailClosed: when validation was skipped (tool
// unavailable), the per-root verdicts default to pass (graph-only) but were never
// structurally validated. The Skipped flag must short-circuit so none of those
// pass verdicts are trusted for acceleration.
func TestReconcileVerdicts_Skipped_FailClosed(t *testing.T) {
	dir := t.TempDir()
	if err := auditionproof.Write(dir, &auditionproof.Results{FluxValidate: &auditionproof.FluxValidate{
		Skipped:  "kustomize unavailable (not pinned)",
		Verdicts: map[string]auditionproof.Verdict{"flux-system/a": {Status: "pass"}},
	}}); err != nil {
		t.Fatal(err)
	}
	validated, _, unavailable := reconcileVerdicts(dir)
	if unavailable == "" {
		t.Fatal("skipped validation must report unavailable even with pass verdicts present")
	}
	if len(validated) != 0 {
		t.Errorf("nothing is accelerable when validation was skipped, got %v", validated)
	}
}
