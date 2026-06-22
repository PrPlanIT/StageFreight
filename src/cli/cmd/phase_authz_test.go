package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
)

func seedSubsystems(t *testing.T, rootDir string, subs ...cistate.SubsystemState) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(rootDir, ".stagefreight"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := cistate.UpdateState(rootDir, func(s *cistate.State) {
		for _, sub := range subs {
			s.RecordSubsystem(sub)
		}
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
}

// TestAuthorizePhase_Publish covers the publish gate (requires build + security).
// The gate reads RAW outcomes: a failed review denies even when AllowFailure is
// set, and only success/warning/skipped/not_applicable authorize.
func TestAuthorizePhase_Publish(t *testing.T) {
	ciCtx := &ci.CIContext{Provider: "gitlab"} // IsCI() == true

	cases := []struct {
		name    string
		subs    []cistate.SubsystemState
		wantErr bool
	}{
		{"build+security success → authorized", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "success"},
			{Name: "security", Attempted: true, Outcome: "success"},
		}, false},
		{"security failed denies (despite AllowFailure)", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "success"},
			{Name: "security", Attempted: true, Outcome: "failed", AllowFailure: true},
		}, true},
		{"security not_applicable (disabled) authorizes", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "success"},
			{Name: "security", Attempted: true, Outcome: "not_applicable"},
		}, false},
		{"security warning authorizes", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "success"},
			{Name: "security", Attempted: true, Outcome: "warning"},
		}, false},
		{"build failed denies", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "failed"},
			{Name: "security", Attempted: true, Outcome: "success"},
		}, true},
		{"security missing denies (did not run)", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "success"},
		}, true},
		{"unknown/incomplete outcome denies (allowlist)", []cistate.SubsystemState{
			{Name: "build", Attempted: true, Outcome: "success"},
			{Name: "security", Attempted: true, Outcome: "pending"},
		}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			seedSubsystems(t, dir, tc.subs...)
			err := authorizePhase(ciCtx, dir, "publish")
			if tc.wantErr && err == nil {
				t.Errorf("expected authorization denied, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected authorized, got %v", err)
			}
		})
	}
}

// TestAuthorizePhase_NotEnforcedLocally: outside CI there are no accumulated
// outcomes to authorize against, so the gate is a no-op even when a failure
// exists — authorizing against empty local state would wrongly block every run.
func TestAuthorizePhase_NotEnforcedLocally(t *testing.T) {
	dir := t.TempDir()
	seedSubsystems(t, dir,
		cistate.SubsystemState{Name: "build", Attempted: true, Outcome: "success"},
		cistate.SubsystemState{Name: "security", Attempted: true, Outcome: "failed"},
	)
	local := &ci.CIContext{} // Provider "" → IsCI() == false
	if err := authorizePhase(local, dir, "publish"); err != nil {
		t.Errorf("local run must not be gated, got %v", err)
	}
}

// TestAuthorizePhase_NoRequirements: a phase with no declared upstream
// requirements is always authorized (the map is the single source of edges).
func TestAuthorizePhase_NoRequirements(t *testing.T) {
	if err := authorizePhase(&ci.CIContext{Provider: "gitlab"}, t.TempDir(), "narrate"); err != nil {
		t.Errorf("phase without requirements should be authorized, got %v", err)
	}
}

// TestAuthorizePhase_MissingStateInCIDenies: a CI run with required upstream but
// no pipeline.json means the upstream evidence never propagated — fail closed.
func TestAuthorizePhase_MissingStateInCIDenies(t *testing.T) {
	if err := authorizePhase(&ci.CIContext{Provider: "gitlab"}, t.TempDir(), "publish"); err == nil {
		t.Errorf("missing state in CI must deny publish authorization")
	}
}
