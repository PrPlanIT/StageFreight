package cmd

import "testing"

// TestAuditionContractRoundTrip proves the WIRING, not just the pure logic: record a contract
// into a real ledger on disk, read it back through cistate serialization, and gate on it —
// the exact path depsRunner (write) → perform (read + gate) takes across two jobs.
func TestAuditionContractRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Remediated: write → read → gate. Must survive serialization and refuse to build.
	// RemediationEnabled: a remediated source is one remediation acted on, so it blocks (the fix
	// is in the Replacement C′, not this subject).
	recordAuditionContract(dir, deriveAuditionContract(auditionInputs{
		RunnerHealthy: true, Remediable: true, RemediationEnabled: true, TestsPassed: true, Replacement: "c0ffee",
	}))
	c := auditionContract(dir)
	if c == nil {
		t.Fatal("contract not read back from the ledger")
	}
	if !c.Blocking || c.Replacement != "c0ffee" {
		t.Fatalf("round-trip lost fields: %+v", c)
	}
	if !c.AllowFailure {
		t.Fatalf("remediated must round-trip AllowFailure (warning badge): %+v", c)
	}
	if build, err := performGate(c); build || err != nil {
		t.Fatalf("remediated must skip-clean (no build, no error): build=%v err=%v", build, err)
	}

	// Clean: upsert over the same ledger, read back, gate → builds.
	recordAuditionContract(dir, deriveAuditionContract(auditionInputs{RunnerHealthy: true, TestsPassed: true}))
	if build, err := performGate(auditionContract(dir)); !build || err != nil {
		t.Fatalf("clean must build: build=%v err=%v", build, err)
	}

	// Unremediable: remediation was enabled but produced no fix → read back, gate → hard fail.
	recordAuditionContract(dir, deriveAuditionContract(auditionInputs{RunnerHealthy: true, Remediable: true, RemediationEnabled: true, TestsPassed: true}))
	if build, err := performGate(auditionContract(dir)); build || err == nil {
		t.Fatalf("unremediable must fail: build=%v err=%v", build, err)
	}

	// Evaluate-only (remediate: false): a remediable finding with remediation DISABLED must round-trip
	// non-blocking and BUILD — the subject ships as-is. This is the image-mode fork path.
	recordAuditionContract(dir, deriveAuditionContract(auditionInputs{RunnerHealthy: true, Remediable: true, RemediationEnabled: false, TestsPassed: true}))
	if build, err := performGate(auditionContract(dir)); !build || err != nil {
		t.Fatalf("remediate:false must build (ship as-is): build=%v err=%v", build, err)
	}
}
