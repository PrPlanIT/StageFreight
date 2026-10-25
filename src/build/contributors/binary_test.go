package contributors

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// TestPublish_ChecksumsArtifactRegistered: a binary-archive target with checksums records a
// SHA256SUMS outcome AND registers its artifact in Outputs, so ResultsBuilder.Build joins
// rather than rejecting an orphan id.
func TestPublish_ChecksumsArtifactRegistered(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "tool")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, build.DistDir), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Targets: []config.TargetConfig{
		{ID: "dev", Kind: "binary-archive", Build: "app", Format: "tar.gz", Checksums: true},
	}}
	b := &binaryContributor{
		version: &gitver.VersionInfo{Version: "0.0.0-test"},
		built:   []builtBinary{{Name: "tool", OS: "linux", Arch: "amd64", Path: binPath, BuildID: "app"}},
	}
	outputs := artifact.OutputsManifest{}
	rb := build.NewResultsBuilder()
	rc := &domains.RunContext{
		Ctx: context.Background(), RootDir: dir, Config: &cfg,
		Writer: io.Discard, Outputs: &outputs, RB: rb,
	}

	contrib, err := b.Publish(rc)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if contrib.Skip {
		t.Fatal("Publish skipped — fixture did not reach the archive path")
	}

	var gotChecksums bool
	for _, a := range outputs.Artifacts {
		if a.Kind == "checksums" && a.Name == "SHA256SUMS" {
			gotChecksums = true
		}
	}
	if !gotChecksums {
		t.Fatal("checksums artifact not registered in Outputs")
	}

	// Finalize mirrors the perform path: it derives each artifact's id from kind+name and
	// computes the checksum, so Build can join the recorded outcomes.
	if err := outputs.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if _, err := rb.Build(&outputs); err != nil {
		t.Fatalf("ResultsBuilder.Build: orphan outcome from checksums: %v", err)
	}
}
