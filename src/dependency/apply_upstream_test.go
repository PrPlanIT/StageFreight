package dependency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

func TestApplyUpstreamUpdates_MovesTogetherOrNotAtAll(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("ARG CORE=2.21.5\nARG OTHER=1.0.0\n"), 0o644)
	os.WriteFile(filepath.Join(root, "versions.yml"), []byte("core: v2.21.5 # pin\n"), 0o644)

	core := []supplychain.Dependency{
		{Name: "core", Group: "core", Ecosystem: supplychain.EcosystemUpstream, File: "Dockerfile", Line: 1, Column: 9, Current: "2.21.5", Latest: "2.21.6"},
		{Name: "core", Group: "core", Ecosystem: supplychain.EcosystemUpstream, File: "versions.yml", Line: 1, Column: 6, Current: "v2.21.5", Latest: "v2.21.6"},
	}
	stale := []supplychain.Dependency{
		{Name: "other", Group: "other", Ecosystem: supplychain.EcosystemUpstream, File: "Dockerfile", Line: 2, Column: 10, Current: "1.0.0", Latest: "1.1.0"},
		{Name: "other", Group: "other", Ecosystem: supplychain.EcosystemUpstream, File: "versions.yml", Line: 1, Column: 6, Current: "0.9.0", Latest: "1.1.0"},
	}
	applied, skipped, touched, err := applyUpstreamUpdates(append(core, stale...), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || len(skipped) != 2 || strings.Join(touched, ",") != "Dockerfile,versions.yml" {
		t.Fatalf("applied=%d skipped=%d touched=%v", len(applied), len(skipped), touched)
	}
	df, _ := os.ReadFile(filepath.Join(root, "Dockerfile"))
	vy, _ := os.ReadFile(filepath.Join(root, "versions.yml"))
	if string(df) != "ARG CORE=2.21.6\nARG OTHER=1.0.0\n" || string(vy) != "core: v2.21.6 # pin\n" {
		t.Fatalf("files:\n%s\n%s", df, vy)
	}
	if !strings.Contains(skipped[0].Reason, "no longer holds 0.9.0") {
		t.Fatalf("a pin that no longer holds its value stops its whole source: %q", skipped[0].Reason)
	}
}
