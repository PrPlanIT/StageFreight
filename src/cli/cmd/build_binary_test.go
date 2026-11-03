package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// --output-dir copies every built binary out as <os>-<arch>/<name>, keeps the dist copy,
// and errors when there is nothing to export.
func TestExportBinaries(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(".stagefreight/dist/linux-amd64/tool")
	write(".stagefreight/dist/linux-arm64/tool")
	arts := []artifact.Artifact{
		{Kind: "binary", Name: "tool-linux-amd64", Binary: &artifact.BinaryDescriptor{OS: "linux", Arch: "amd64", Path: ".stagefreight/dist/linux-amd64/tool"}},
		{Kind: "binary", Name: "tool-linux-arm64", Binary: &artifact.BinaryDescriptor{OS: "linux", Arch: "arm64", Path: filepath.Join(root, ".stagefreight/dist/linux-arm64/tool")}},
		{Kind: "docker", Name: "image"},
	}
	out := filepath.Join(t.TempDir(), "bin")
	exported, err := exportBinaries(root, out, arts)
	if err != nil || len(exported) != 2 {
		t.Fatalf("exported=%v err=%v, want both platforms", exported, err)
	}
	for _, rel := range []string{"linux-amd64/tool", "linux-arm64/tool"} {
		info, err := os.Stat(filepath.Join(out, rel))
		if err != nil {
			t.Fatalf("%s not exported: %v", rel, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s lost its executable bit", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".stagefreight/dist/linux-amd64/tool")); err != nil {
		t.Error("the dist copy must stay in place")
	}
	if _, err := exportBinaries(root, out, []artifact.Artifact{{Kind: "docker"}}); err == nil {
		t.Error("nothing to export must be an error")
	}
}
