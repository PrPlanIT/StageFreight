package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

// Regression: a vendor marker (.cargo_vcs_info.json) marks its directory vendored even
// when --level changed scans ONLY a file beneath it and not the marker itself. Without
// deriving vendor roots from the full collected set, the changed vendored file would be
// mis-classified authored and emit hygiene noise — the exact regression provenance fixes.
func TestProvenanceVendoredSurvivesDeltaFilter(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vend"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "vend", ".cargo_vcs_info.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "vend", "x.css"), []byte("a {} \n"), 0o644) // trailing ws

	eng, err := lint.NewEngine(config.LintConfig{}, dir, []string{"lineendings"}, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	all, err := eng.CollectFiles() // populates the full-set provenance basis (incl. the marker)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate --level changed: scan ONLY the vendored file; the marker is NOT in the subset.
	var subset []lint.FileInfo
	for _, f := range all {
		if filepath.ToSlash(f.Path) == "vend/x.css" {
			subset = append(subset, f)
		}
	}
	if len(subset) != 1 {
		t.Fatalf("expected to find vend/x.css, got %d", len(subset))
	}

	findings, _, err := eng.RunWithStats(context.Background(), subset)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Module == "lineendings" {
			t.Errorf("vendored file must relax hygiene even under delta filter; got %+v", f)
		}
	}
}
