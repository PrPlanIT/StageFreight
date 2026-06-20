package docker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestStageBuildBinaries_RecyclesByArch(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "produced")
	if err := os.WriteFile(bin, []byte("\x7fELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := filepath.Join(dir, "ctx")
	if err := os.MkdirAll(ctx, 0o755); err != nil {
		t.Fatal(err)
	}
	rc := &domains.RunContext{
		Config: &config.Config{Builds: []config.BuildConfig{
			{ID: "img", Kind: "docker", Context: ctx, Stage: &config.StageConfig{From: "bin", As: "jetpack-{arch}"}},
		}},
		Outputs: &artifact.OutputsManifest{Artifacts: []artifact.Artifact{
			{Kind: "binary", Binary: &artifact.BinaryDescriptor{OS: "linux", Arch: "amd64", Path: bin, BuildID: "bin"}},
		}},
	}
	if err := stageBuildBinaries(rc); err != nil {
		t.Fatalf("stage: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ctx, "jetpack-amd64")); err != nil {
		t.Errorf("expected jetpack-amd64 staged into context: %v", err)
	}
}

func TestStageBuildBinaries_ErrorsWhenNoBinary(t *testing.T) {
	rc := &domains.RunContext{
		Config:  &config.Config{Builds: []config.BuildConfig{{ID: "img", Kind: "docker", Stage: &config.StageConfig{From: "missing", As: "x"}}}},
		Outputs: &artifact.OutputsManifest{},
	}
	if err := stageBuildBinaries(rc); err == nil {
		t.Error("expected error when stage.from produced no binary")
	}
}

func TestSubstituteStageName(t *testing.T) {
	if got := substituteStageName("app-{os}-{arch}", "linux", "arm64"); got != "app-linux-arm64" {
		t.Errorf("got %q", got)
	}
}
