package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestAssertRequiredEvidence covers narrate's fail-closed guard: a required upstream
// (build/security) that recorded NO outcome must be synthesized as a failing subsystem
// so the run narrates as failing (postmortem) rather than silently clean. This is what
// keeps the tolerant artifact downloads (which let narrate survive a killed producer)
// from becoming a silent bypass — a crashed security scan must never narrate green.
func TestAssertRequiredEvidence(t *testing.T) {
	image := &config.Config{Lifecycle: config.LifecycleConfig{Mode: config.ModeImage}}
	gitops := &config.Config{Lifecycle: config.LifecycleConfig{Mode: config.ModeGitops}}

	t.Run("image mode: absent security narrates failing", func(t *testing.T) {
		dir := t.TempDir()
		seedSubsystems(t, dir, cistate.SubsystemState{Name: "build", Attempted: true, Outcome: "success"})
		assertRequiredEvidence(image, dir)
		st, err := cistate.ReadState(dir)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if got := st.PipelineStatus(); got != "failing" {
			t.Errorf("missing security must narrate as failing, got %q", got)
		}
		s := st.GetSubsystem("security")
		if s == nil || s.Outcome != "failed" || !s.Required {
			t.Errorf("security must be synthesized as required+failed, got %+v", s)
		}
	})

	t.Run("image mode: present evidence preserved and passes", func(t *testing.T) {
		dir := t.TempDir()
		seedSubsystems(t, dir,
			cistate.SubsystemState{Name: "build", Attempted: true, Outcome: "success"},
			cistate.SubsystemState{Name: "security", Attempted: true, Outcome: "success"},
		)
		assertRequiredEvidence(image, dir)
		st, err := cistate.ReadState(dir)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if s := st.GetSubsystem("security"); s == nil || s.Outcome != "success" {
			t.Errorf("existing security outcome must not be clobbered, got %+v", s)
		}
		if got := st.PipelineStatus(); got != "passing" {
			t.Errorf("all required present+success → passing, got %q", got)
		}
	})

	t.Run("reconcile mode: absent build/security is expected, not synthesized", func(t *testing.T) {
		dir := t.TempDir()
		seedSubsystems(t, dir, cistate.SubsystemState{Name: "reconcile", Attempted: true, Outcome: "success"})
		assertRequiredEvidence(gitops, dir)
		st, err := cistate.ReadState(dir)
		if err != nil {
			t.Fatalf("read state: %v", err)
		}
		if s := st.GetSubsystem("security"); s != nil {
			t.Errorf("reconcile mode must not synthesize security evidence, got %+v", s)
		}
		if got := st.PipelineStatus(); got != "passing" {
			t.Errorf("reconcile success → passing, got %q", got)
		}
	})
}
