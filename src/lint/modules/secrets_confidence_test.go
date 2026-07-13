package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// A CPUID vendor-magic comment is a hex/numeric code constant — objectively not a secret. It
// must be CLASSIFIED OUT (no finding at all), not flagged-and-then-down-gated.
func TestSecrets_CodeConstantNotFlagged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hw.rs")
	os.WriteFile(p, []byte(`    // "AuthenticAMD" = EBX=0x68747541 EDX=0x69746E65 ECX=0x444D4163`+"\n"), 0o644)
	got, err := (&secretsModule{}).Check(context.Background(), lint.FileInfo{Path: "hw.rs", AbsPath: p})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Module == "secrets" {
			t.Errorf("a hex/numeric code constant must not be flagged as a secret: %+v", f)
		}
	}
}

// Confidence sets review priority AND gates severity (see severity test below).
func TestSecretConfidence_Calibration(t *testing.T) {
	if secretConfidence("generic-api-key", 3.52) != lint.ConfidenceHeuristic {
		t.Error("low-entropy generic-api-key → heuristic (review priority)")
	}
	if secretConfidence("generic-api-key", 5.1) != lint.ConfidenceProbable {
		t.Error("high-entropy generic-api-key → probable")
	}
	if secretConfidence("aws-access-token", 3.0) != lint.ConfidenceConfirmed {
		t.Error("specific provider rule → confirmed")
	}
}

// Confirmed credentials block (Critical/fatal); Probable and Heuristic hits — the
// generic-api-key false-positive-prone class — are Warnings: surfaced but non-blocking.
func TestSecrets_SeverityGatedOnConfidence(t *testing.T) {
	if got := severityForConfidence(lint.ConfidenceConfirmed); got != lint.SeverityCritical || !(lint.Finding{Severity: got}).Blocks() {
		t.Errorf("confirmed → critical/blocking, got %v", got)
	}
	for _, c := range []lint.Confidence{lint.ConfidenceProbable, lint.ConfidenceHeuristic} {
		got := severityForConfidence(c)
		if got != lint.SeverityWarning || (lint.Finding{Severity: got}).Blocks() {
			t.Errorf("%v → warning/non-blocking, got %v", c, got)
		}
	}
}
