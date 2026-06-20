package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// Authored-hygiene modules must relax on non-authored provenance — but only the hygiene
// modules. The control case (authored) must still fire, proving it's the gate, not a
// no-op detector.
func TestHygieneModulesRelaxOnProvenance(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.css")
	if err := os.WriteFile(p, []byte("a {} \nb {}  \n"), 0o644); err != nil { // trailing ws
		t.Fatal(err)
	}
	authored := lint.FileInfo{Path: "x.css", AbsPath: p, Content: lint.Content{Kind: lint.ContentText}}
	vendored := authored
	vendored.Provenance = lint.Provenance{Kind: lint.ProvenanceVendored}
	generated := authored
	generated.Provenance = lint.Provenance{Kind: lint.ProvenanceGenerated}

	le := &lineEndingsModule{}
	if got, _ := le.Check(context.Background(), authored); len(got) == 0 {
		t.Fatal("control: authored .css with trailing whitespace must produce findings")
	}
	if got, _ := le.Check(context.Background(), vendored); len(got) != 0 {
		t.Errorf("lineendings must relax on vendored, got %d findings", len(got))
	}
	if got, _ := le.Check(context.Background(), generated); len(got) != 0 {
		t.Errorf("lineendings must relax on generated, got %d findings", len(got))
	}
}
