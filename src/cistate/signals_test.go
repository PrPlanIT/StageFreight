package cistate

import (
	"strings"
	"testing"
)

func TestSignals_UnionAcrossSubsystems(t *testing.T) {
	st := &State{Subsystems: []SubsystemState{
		{Name: "upstream", Results: map[string]string{SignalsKey: "updates-available,version-mismatch"}},
		{Name: "test"},
		{Name: "other", Results: map[string]string{SignalsKey: "version-mismatch, upstream-unreachable"}},
	}}
	if got := strings.Join(st.Signals(), ","); got != "updates-available,version-mismatch,upstream-unreachable" {
		t.Fatalf("signals = %s", got)
	}
	var none *State
	if none.Signals() != nil {
		t.Fatal("a nil state raised signals")
	}
}
