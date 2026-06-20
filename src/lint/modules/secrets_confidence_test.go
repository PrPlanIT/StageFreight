package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// The CPUID vendor-magic comment (a real dragonfly false positive) is a low-entropy
// generic-api-key match — it must surface as a WARNING, never a CI-failing CRITICAL.
func TestSecrets_LowEntropyGenericKeyNotCritical(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hw.rs")
	os.WriteFile(p, []byte(`    // "AuthenticAMD" = EBX=0x68747541 EDX=0x69746E65 ECX=0x444D4163`+"\n"), 0o644)

	got, err := (&secretsModule{}).Check(context.Background(), lint.FileInfo{Path: "hw.rs", AbsPath: p})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		if f.Module == "secrets" && f.Severity == lint.SeverityCritical {
			t.Errorf("CPUID magic number (low-entropy generic-api-key) must not be CRITICAL: %+v", f)
		}
	}
}

func TestSecretSeverity_Calibration(t *testing.T) {
	if secretSeverity("generic-api-key", 3.52) != lint.SeverityWarning {
		t.Error("low-entropy generic-api-key should be warning")
	}
	if secretSeverity("generic-api-key", 5.1) != lint.SeverityCritical {
		t.Error("high-entropy generic-api-key should be critical")
	}
	if secretSeverity("aws-access-token", 3.0) != lint.SeverityCritical {
		t.Error("a specific provider rule is always critical regardless of entropy")
	}
}
