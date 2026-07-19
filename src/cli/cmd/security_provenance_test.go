package cmd

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// writeOutputsWithCommit writes an outputs.json carrying a commit + a persisted
// docker layout — the shape review reads on the content-store path.
func writeOutputsWithCommit(t *testing.T, rootDir, commit string, digest artifact.Digest, layoutDir string) {
	t.Helper()
	m := artifact.OutputsManifest{
		Commit: commit,
		Artifacts: []artifact.Artifact{{
			Kind: "docker", Name: "app", Digest: digest,
			Docker: &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Persistence: artifact.PersistenceHandle{
				Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: layoutDir},
			},
			Targets: []artifact.Target{{Kind: "registry", Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "org/app", Tags: []string{"v1"}}}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(rootDir, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(rootDir, m); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
}

// TestResolveCASTarget_CarriesExpectedCommit proves review now propagates the
// artifact's build commit into the scan target — without it, the downstream
// provenance gate has nothing to check.
func TestResolveCASTarget_CarriesExpectedCommit(t *testing.T) {
	root := t.TempDir()
	layoutDir, digest := writeValidLayout(t, []byte("bytes-for-commit-test"))
	writeOutputsWithCommit(t, root, "deadbeefcafe1234", digest, layoutDir)

	target, _, ok := resolveCASTarget(root, io.Discard)
	if !ok {
		t.Fatal("resolveCASTarget did not resolve a verified layout")
	}
	if target.ExpectedCommit != "deadbeefcafe1234" {
		t.Fatalf("ExpectedCommit = %q, want deadbeefcafe1234", target.ExpectedCommit)
	}
}

// TestCASCommitMismatch pins the fail-closed provenance rule: a carried artifact
// whose build commit differs from the pipeline commit is a mismatch (review must
// refuse to scan it), while every "nothing to enforce" case is not.
func TestCASCommitMismatch(t *testing.T) {
	tests := []struct {
		name         string
		targetCommit string
		ciSHA        string
		wantMismatch bool
	}{
		{"not in CI (no SF_CI_SHA) -> no enforcement", "abc123", "", false},
		{"artifact records no commit -> no enforcement", "", "abc123", false},
		{"commits agree -> ok", "abc123", "abc123", false},
		{"commits differ -> MISMATCH (fail closed)", "abc123", "def456", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, mismatch := casCommitMismatch(tt.targetCommit, tt.ciSHA)
			if mismatch != tt.wantMismatch {
				t.Fatalf("mismatch = %v, want %v (msg=%q)", mismatch, tt.wantMismatch, msg)
			}
			if mismatch && msg == "" {
				t.Fatal("mismatch reported with an empty reason")
			}
			if !mismatch && msg != "" {
				t.Fatalf("no mismatch but got a reason: %q", msg)
			}
		})
	}
}
