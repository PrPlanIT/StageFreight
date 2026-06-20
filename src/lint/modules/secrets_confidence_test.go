package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// The CPUID vendor-magic comment is a low-entropy generic-api-key match: its severity stays
// CRITICAL (impact if real), but its confidence is Heuristic so it does NOT block CI.
func TestSecrets_LowEntropyGenericKeyDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hw.rs")
	os.WriteFile(p, []byte(`    // "AuthenticAMD" = EBX=0x68747541 EDX=0x69746E65 ECX=0x444D4163`+"\n"), 0o644)

	got, err := (&secretsModule{}).Check(context.Background(), lint.FileInfo{Path: "hw.rs", AbsPath: p})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Module != "secrets" {
			continue
		}
		if f.Severity != lint.SeverityCritical {
			t.Errorf("severity should remain critical (impact axis): %+v", f)
		}
		if f.Confidence != lint.ConfidenceHeuristic {
			t.Errorf("low-entropy generic-api-key should be heuristic confidence: %+v", f)
		}
		if f.Blocks() {
			t.Errorf("CPUID magic number must NOT block CI: %+v", f)
		}
	}
}

func TestSecretConfidence_Calibration(t *testing.T) {
	if secretConfidence("generic-api-key", 3.52) != lint.ConfidenceHeuristic {
		t.Error("low-entropy generic-api-key → heuristic")
	}
	if secretConfidence("generic-api-key", 5.1) != lint.ConfidenceProbable {
		t.Error("high-entropy generic-api-key → probable")
	}
	if secretConfidence("aws-access-token", 3.0) != lint.ConfidenceConfirmed {
		t.Error("specific provider rule → confirmed regardless of entropy")
	}
}
