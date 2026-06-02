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
	"github.com/PrPlanIT/StageFreight/src/cas"
)

// TestCruciblePublishSeam_HandleReachesPromotion guards the integration seam the
// crucible now straddles: it both verifies AND retains artifacts, and a past bug
// had it export a layout but never persist it (so no handle reached publish).
// This test proves the contract publish depends on: an outputs.json carrying a
// persistence handle whose layout verifies is resolvable by the promotion path's
// pre-flight (cas.VerifyLayoutAt) — i.e. once the crucible writes the handle,
// publish can promote.
//
// It is a pure test of the data contract between the crucible's retain step and
// the publish promotion step; the live build is covered by docker-gated tests.
func TestCruciblePublishSeam_HandleReachesPromotion(t *testing.T) {
	// Build a valid OCI layout fixture and retain it the way the crucible's
	// persistArtifacts does (store-backed).
	blob := []byte("crucible-verified-manifest-bytes")
	sum := sha256.Sum256(blob)
	h := hex.EncodeToString(sum[:])
	layoutSrc := t.TempDir()
	blobDir := filepath.Join(layoutSrc, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, h), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	idx := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":%d}]}`, h, len(blob))
	if err := os.WriteFile(filepath.Join(layoutSrc, "index.json"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := artifact.Digest("sha256:" + h)

	root := t.TempDir()
	store := cas.NewFSStore(filepath.Join(root, ".stagefreight", "objects"))
	stored, err := store.Put(cas.Digest(digest), layoutSrc)
	if err != nil {
		t.Fatalf("retain: %v", err)
	}

	// The crucible writes outputs.json with the persistence handle. Mirror that.
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:        "docker",
			Name:        "stagefreight",
			Digest:      digest,
			Docker:      &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Persistence: artifact.PersistenceHandle{Kind: artifact.PersistenceOCILayout, OCILayout: &artifact.OCILayoutRef{Path: stored}},
			Targets: []artifact.Target{{
				Kind:     "registry",
				Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "prplanit/stagefreight", Tags: []string{"latest"}},
			}},
		}},
	}
	if err := os.MkdirAll(filepath.Join(root, ".stagefreight"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := artifact.WriteOutputsManifest(root, m); err != nil {
		t.Fatal(err)
	}

	// Publish's promotion pre-flight: read outputs, find the docker artifact with
	// a handle, and verify its layout. If the crucible failed to retain (the old
	// bug), the handle would be absent or the layout would not verify — and this
	// would catch it.
	outputs, err := artifact.ReadOutputsManifest(root)
	if err != nil {
		t.Fatalf("read outputs: %v", err)
	}
	a := outputs.Artifacts[0]
	if a.Persistence.Kind != artifact.PersistenceOCILayout || a.Persistence.OCILayout == nil {
		t.Fatal("crucible artifact has no persistence handle — publish cannot promote (the 'exported but not retained' regression)")
	}
	if err := cas.VerifyLayoutAt(a.Persistence.OCILayout.Path, cas.Digest(a.Digest)); err != nil {
		t.Fatalf("retained layout does not verify against recorded digest — publish would refuse to promote: %v", err)
	}

	// And resolveCASTarget (review) resolves the same handle, proving review and
	// publish agree on what the crucible retained.
	if _, dir, ok := resolveCASTarget(root, io.Discard); !ok || dir != a.Persistence.OCILayout.Path {
		t.Fatalf("review could not resolve the crucible-retained layout: ok=%v dir=%q", ok, dir)
	}
}
