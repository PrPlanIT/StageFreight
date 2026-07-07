package lint

import "testing"

func TestClassify(t *testing.T) {
	findings := []Finding{
		{Module: "freshness", Severity: SeverityCritical}, // blocking, world → remediable
		{Module: "osv", Severity: SeverityCritical},       // blocking, world → remediable
		{Module: "secrets", Severity: SeverityCritical},   // blocking, non-world → fatal (voids source)
		{Module: "freshness", Severity: SeverityWarning},  // non-blocking → ignored
		{Module: "tabs", Severity: SeverityInfo},          // non-blocking → ignored
	}
	m := Classify(findings)

	if len(m.Remediable) != 2 {
		t.Fatalf("Remediable = %d, want 2 (freshness, osv)", len(m.Remediable))
	}
	if len(m.Fatal) != 1 || m.Fatal[0].Module != "secrets" {
		t.Fatalf("Fatal = %v, want exactly [secrets]", m.Fatal)
	}
	if !m.HasFatal() || !m.HasRemediable() {
		t.Fatalf("HasFatal=%v HasRemediable=%v, want both true", m.HasFatal(), m.HasRemediable())
	}
}

func TestClassify_CleanTree(t *testing.T) {
	// No findings, and non-blocking-only, are both "clean" for mutation safety.
	cases := map[string][]Finding{
		"empty":         nil,
		"warnings-only": {{Module: "freshness", Severity: SeverityWarning}, {Module: "tabs", Severity: SeverityInfo}},
	}
	for name, fs := range cases {
		m := Classify(fs)
		if m.HasFatal() || m.HasRemediable() {
			t.Errorf("%s: HasFatal=%v HasRemediable=%v, want both false", name, m.HasFatal(), m.HasRemediable())
		}
	}
}

func TestClassify_RemediableOnlyIsMutable(t *testing.T) {
	// The crux case: a blocking world finding with no fatal finding is safe to mutate —
	// exactly the chicken-egg the old gate aborted on before running the remediator.
	m := Classify([]Finding{{Module: "osv", Severity: SeverityCritical}})
	if m.HasFatal() {
		t.Fatalf("remediable-only must not be fatal: %v", m.Fatal)
	}
	if !m.HasRemediable() {
		t.Fatal("remediable-only must expose a remediable finding to mutate")
	}
}
