package cistate

import "testing"

// TestFact covers the fact-resolution semantics: bare status facts are
// status-carrying; dotted tokens ALWAYS resolve (empty when the domain recorded
// nothing — presence-elision); unknown bare tokens stay unresolved (literal).
func TestFact(t *testing.T) {
	st := &State{Version: 1}
	st.RecordSubsystem(SubsystemState{
		Name: "test", Attempted: true, Completed: true, Outcome: "success",
		Results: map[string]string{"passed": "142", "failed": "0", "total": "142", "coverage": "78.4%"},
	})
	st.RecordSubsystem(SubsystemState{Name: "security", Attempted: true, Outcome: "skipped", Reason: "tests gate failed"})

	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"status", "passed", true},
		{"status_icon", "✅", true},
		{"status_verb", "Passed", true},
		{"tests.passed", "142", true},         // domain alias tests → subsystem "test"
		{"tests.coverage", "78.4%", true},     //
		{"security.outcome", "skipped", true}, // lifecycle fields as facts
		{"security.reason", "tests gate failed", true},
		{"security.blocking", "", true}, // known domain, unrecorded key → elides
		{"publish.tags", "", true},      // domain never ran → elides
		{"failure.subsystem", "", true}, // nothing failed → elides
		{"retention.pruned", "", true},  // zero pruned → elides
		{"nonsense", "", false},         // unknown bare → literal
	}
	for _, tc := range cases {
		got, ok := st.Fact(tc.name)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Fact(%q) = (%q, %v), want (%q, %v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// TestFact_Failure covers the failure.* derivation (first hard failure wins over a
// soft one) and status alternation on a failing run.
func TestFact_Failure(t *testing.T) {
	st := &State{Version: 1}
	st.RecordSubsystem(SubsystemState{Name: "mirror", Attempted: true, Outcome: "failed", Reason: "sync refused", AllowFailure: true})
	st.RecordSubsystem(SubsystemState{Name: "test", Attempted: true, Outcome: "failed", Reason: "3 of 142 tests failed"})

	if got, _ := st.Fact("failure.subsystem"); got != "test" {
		t.Errorf("failure.subsystem = %q, want the hard failure %q", got, "test")
	}
	if got, _ := st.Fact("failure.reason"); got != "3 of 142 tests failed" {
		t.Errorf("failure.reason = %q", got)
	}
	if got, _ := st.Fact("status"); got != "failed" {
		t.Errorf("status = %q, want failed", got)
	}
	if got, _ := st.Fact("status_icon"); got != "🚨" {
		t.Errorf("status_icon = %q, want 🚨", got)
	}
}

// TestRecordSubsystem_PreservesResults pins the merge contract: a lifecycle upsert
// with nil Results keeps previously recorded facts; MergeSubsystemResults never
// touches lifecycle fields.
func TestRecordSubsystem_PreservesResults(t *testing.T) {
	st := &State{Version: 1}
	st.MergeSubsystemResults("security", map[string]string{"blocking": "0", "low": "3"})
	st.RecordSubsystem(SubsystemState{Name: "security", Attempted: true, Completed: true, Outcome: "success"})

	sub := st.GetSubsystem("security")
	if sub == nil || sub.Outcome != "success" || !sub.Attempted {
		t.Fatalf("lifecycle fields lost: %+v", sub)
	}
	if sub.Results["blocking"] != "0" || sub.Results["low"] != "3" {
		t.Errorf("outcome upsert erased recorded facts: %+v", sub.Results)
	}

	st.MergeSubsystemResults("security", map[string]string{"low": "4", "sbom": "✓"})
	sub = st.GetSubsystem("security")
	if sub.Outcome != "success" || sub.Results["low"] != "4" || sub.Results["sbom"] != "✓" || sub.Results["blocking"] != "0" {
		t.Errorf("merge semantics wrong: %+v", sub)
	}
}
