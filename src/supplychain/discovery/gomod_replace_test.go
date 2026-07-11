package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// TestCheckGoMod_HonorsReplace: a replace-governed module is marked Pinned and its
// version is NOT resolved (so it is never reported outdated).
func TestCheckGoMod_HonorsReplace(t *testing.T) {
	dir := t.TempDir()
	gomod := filepath.Join(dir, "go.mod")
	content := "module example.com/test\n\ngo 1.26\n\nrequire (\n\texample.com/pinned v1.0.0\n\texample.com/other v2.0.0 // indirect\n)\n\nreplace example.com/pinned => example.com/pinned v1.0.0\n"
	if err := os.WriteFile(gomod, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	deps, err := NewResolver().checkGoMod(context.Background(), lint.FileInfo{Path: "go.mod", AbsPath: gomod})
	if err != nil {
		t.Fatal(err)
	}
	var pinned *supplychain.Dependency
	for i := range deps {
		if deps[i].Name == "example.com/pinned" {
			pinned = &deps[i]
		}
	}
	if pinned == nil {
		t.Fatal("pinned dep not found in deps")
	}
	if pinned.Pinned == "" {
		t.Error("replace-governed module must be marked Pinned")
	}
	if pinned.Latest != "" {
		t.Errorf("pinned dep must skip resolution (Latest empty), got %q", pinned.Latest)
	}
}

func TestReplacedModule(t *testing.T) {
	cases := map[string]string{
		"example.com/a v1 => ./local":       "example.com/a",
		"example.com/a => example.com/b v2": "example.com/a",
		"no arrow here":                     "",
	}
	for in, want := range cases {
		if got := replacedModule(in); got != want {
			t.Errorf("replacedModule(%q) = %q, want %q", in, got, want)
		}
	}
}
