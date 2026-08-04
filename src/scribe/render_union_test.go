package scribe

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/stencil"
)

func reconcileSub() cistate.SubsystemState {
	return cistate.SubsystemState{
		Name: "reconcile", Attempted: true, Completed: true, Required: true,
		Outcome: "success",
		Results: map[string]string{
			"succeeded": "42", "total": "42", "units": "kustomizations", "cluster": "dungeon",
		},
	}
}

func ansibleSub() cistate.SubsystemState {
	return cistate.SubsystemState{
		Name: "ansible", Attempted: true, Completed: true, Required: true,
		Outcome: "success",
		Results: map[string]string{"converged": "10", "total": "10", "changed": "3"},
	}
}

func renderShippedSummary(t *testing.T, st *cistate.State) string {
	t.Helper()
	def, ok := config.ShippedStencil("summary", "gitops")
	if !ok {
		t.Fatal("shipped summary missing")
	}
	return stencil.Expand(def.Body, stencil.Env{
		Resolve: func(name string) (string, bool) { return st.Fact(name) },
	})
}

// TestUnionBody_GitopsAndAnsibleCompose pins the union doctrine for the new
// modality: one shipped body renders gitops-only, ansible-only, and both —
// each modality's line eliding independently when its subsystem is absent.
func TestUnionBody_GitopsAndAnsibleCompose(t *testing.T) {
	both := &cistate.State{}
	both.RecordSubsystem(reconcileSub())
	both.RecordSubsystem(ansibleSub())
	out := renderShippedSummary(t, both)
	if !strings.Contains(out, "Converged 42/42 kustomizations on dungeon") {
		t.Errorf("gitops line missing:\n%s", out)
	}
	if !strings.Contains(out, "Converged 10/10 nodes · 3 changed") {
		t.Errorf("ansible line missing:\n%s", out)
	}

	ansibleOnly := &cistate.State{}
	ansibleOnly.RecordSubsystem(ansibleSub())
	out = renderShippedSummary(t, ansibleOnly)
	if strings.Contains(out, "kustomizations") {
		t.Errorf("gitops line must elide without a reconcile subsystem:\n%s", out)
	}
	if !strings.Contains(out, "Converged 10/10 nodes · 3 changed") {
		t.Errorf("ansible line missing in ansible-only run:\n%s", out)
	}

	gitopsOnly := &cistate.State{}
	gitopsOnly.RecordSubsystem(reconcileSub())
	out = renderShippedSummary(t, gitopsOnly)
	if strings.Contains(out, "nodes") {
		t.Errorf("ansible line must elide without an ansible subsystem:\n%s", out)
	}
}

// TestUnionBody_AnsibleFailureNarrates pins the automatic failure integration:
// a failed converge drives {failure.subsystem}/{failure.reason} with no new
// wiring, and the ansible.* facts stay resolvable for the postmortem body.
func TestUnionBody_AnsibleFailureNarrates(t *testing.T) {
	st := &cistate.State{}
	sub := ansibleSub()
	sub.Outcome = "failed"
	sub.Reason = "2 unreachable, 0 failed of 10 hosts"
	sub.Results["converged"] = "8"
	sub.Results["unreachable"] = "2"
	st.RecordSubsystem(sub)

	if v, _ := st.Fact("failure.subsystem"); v != "ansible" {
		t.Errorf("failure.subsystem = %q", v)
	}
	if v, _ := st.Fact("failure.reason"); !strings.Contains(v, "unreachable") {
		t.Errorf("failure.reason = %q", v)
	}
	if v, _ := st.Fact("ansible.converged"); v != "8" {
		t.Errorf("ansible.converged = %q", v)
	}
	if v, _ := st.Fact("ansible.unreachable"); v != "2" {
		t.Errorf("ansible.unreachable = %q", v)
	}
}
