package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// writeValidLayout writes a minimal valid OCI layout (index.json referencing a
// manifest blob that hashes to its name) and returns (dir, digest).
func writeValidLayout(t *testing.T, blob []byte) (string, artifact.Digest) {
	t.Helper()
	sum := sha256.Sum256(blob)
	h := hex.EncodeToString(sum[:])
	dir := t.TempDir()
	blobDir := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, h), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	idx := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":%d}]}`, h, len(blob))
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, artifact.Digest("sha256:" + h)
}

// writeOutputsWithPersistence writes an outputs.json under rootDir containing a
// single docker artifact with the given digest + persistence handle.
func writeOutputsWithPersistence(t *testing.T, rootDir string, digest artifact.Digest, layoutDir string) {
	t.Helper()
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:   "docker",
			Name:   "app",
			Digest: digest,
			Docker: &artifact.DockerDescriptor{
				Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"},
			},
			Persistence: artifact.PersistenceHandle{
				Kind:      artifact.PersistenceOCILayout,
				OCILayout: &artifact.OCILayoutRef{Path: layoutDir},
			},
			Targets: []artifact.Target{{
				Kind: "registry",
				Registry: &artifact.RegistryTarget{
					Host: "docker.io", Path: "org/app", Tags: []string{"v1"},
				},
			}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(rootDir, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(rootDir, m); err != nil {
		t.Fatalf("write outputs: %v", err)
	}
}

// TestResolveCASTarget_PicksVerifiedLayout proves the review inversion: when
// outputs declares a docker artifact with a digest and a persisted OCI layout
// that re-hash-verifies, review resolves the content-store layout (no
// publication required).
func TestResolveCASTarget_PicksVerifiedLayout(t *testing.T) {
	root := t.TempDir()
	layoutDir, digest := writeValidLayout(t, []byte("manifest-bytes-for-app"))
	writeOutputsWithPersistence(t, root, digest, layoutDir)

	target, dir, ok := resolveCASTarget(root, io.Discard)
	if !ok {
		t.Fatal("resolveCASTarget did not resolve a verified persisted layout")
	}
	if dir != layoutDir {
		t.Fatalf("layout dir = %q, want %q", dir, layoutDir)
	}
	if target.Digest != string(digest) {
		t.Fatalf("target digest = %q, want %q", target.Digest, digest)
	}
	if target.Stability != "digest" {
		t.Fatalf("stability = %q, want digest (content-addressed)", target.Stability)
	}
}

// TestResolveCASTarget_TamperedLayoutFallsBack proves review never scans bytes
// it cannot verify: if the persisted layout is corrupted, resolveCASTarget
// reports ok=false so the caller falls back rather than trusting a bad claim.
func TestResolveCASTarget_TamperedLayoutFallsBack(t *testing.T) {
	root := t.TempDir()
	layoutDir, digest := writeValidLayout(t, []byte("good-manifest-bytes"))
	writeOutputsWithPersistence(t, root, digest, layoutDir)

	// Corrupt the blob so its content no longer hashes to its filename.
	h := string(digest)[len("sha256:"):]
	if err := os.WriteFile(filepath.Join(layoutDir, "blobs", "sha256", h), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, ok := resolveCASTarget(root, io.Discard)
	if ok {
		t.Fatal("resolveCASTarget trusted a tampered layout — must fall back, never scan unverified bytes")
	}
}

// TestResolveCASTarget_NoHandleFallsBack proves the legacy path is used when an
// artifact has a digest but no persistence handle (e.g. content store not
// active in perform).
func TestResolveCASTarget_NoHandleFallsBack(t *testing.T) {
	root := t.TempDir()
	_, digest := writeValidLayout(t, []byte("bytes"))
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:    "docker",
			Name:    "app",
			Digest:  digest, // digest present, but NO persistence handle
			Docker:  &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Targets: []artifact.Target{{Kind: "registry", Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "org/app", Tags: []string{"v1"}}}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(root, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(root, m); err != nil {
		t.Fatal(err)
	}

	if _, _, ok := resolveCASTarget(root, io.Discard); ok {
		t.Fatal("resolveCASTarget resolved without a persistence handle — should fall back")
	}
}
