package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
)

// TestPerformRetainsToCAS_Live proves store activation end to end: a real build
// with an FSStore retains the image's OCI layout in the content store under the
// recorded digest, the layout verifies on read, and the same digest is what
// perform recorded. This is the activation proof — perform actually populates
// CAS so review and publish can resolve the carried bytes.
//
// Docker-gated: builds a real image. Skips in the pure suite.
func TestPerformRetainsToCAS_Live(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker not available — CAS activation proof runs only on a docker-capable runner")
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"),
		[]byte("FROM alpine:3.19\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	storeRoot := t.TempDir()
	store := cas.NewFSStore(storeRoot)

	// Drive a single load build directly through executePhase's seam: build a
	// step with a layout dir + store, then exercise the same retain path perform
	// uses. We call the lower-level pieces to keep the test focused on retention.
	layoutDir := t.TempDir()
	metaFile := filepath.Join(t.TempDir(), "m.json")
	step := build.BuildStep{
		Name:         "app",
		Dockerfile:   filepath.Join(work, "Dockerfile"),
		Context:      work,
		Output:       build.OutputImage,
		Platforms:    []string{"linux/amd64"},
		Tags:         []string{"sf-cas-activation:probe"},
		Load:         true,
		OCILayoutDir: layoutDir,
		MetadataFile: metaFile,
	}
	bx := NewBuildx(false)
	var out strings.Builder
	bx.Stdout = &out
	bx.Stderr = &out
	if _, err := bx.Build(context.Background(), step); err != nil {
		t.Fatalf("build: %v\n%s", err, out.String())
	}
	defer exec.Command("docker", "image", "rm", "-f", "sf-cas-activation:probe").Run()

	digest, err := ParseMetadataDigest(metaFile)
	if err != nil {
		t.Fatalf("metadata digest: %v", err)
	}

	// Build a one-artifact outputs manifest and run the real retain helper.
	outputs := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{{
			Kind:   "docker",
			Name:   "app",
			Digest: artifact.Digest(digest),
			Docker: &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
			Targets: []artifact.Target{{Kind: "registry", Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "org/app", Tags: []string{"v1"}}}},
		}},
	}
	if err := outputs.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	plan := &build.BuildPlan{Steps: []build.BuildStep{step}}

	persistArtifacts(plan, &outputs, store, &out, false)

	// The artifact must now carry a persistence handle pointing at a stored
	// layout that verifies against the recorded digest.
	h := outputs.Artifacts[0].Persistence
	if h.Kind != artifact.PersistenceOCILayout || h.OCILayout == nil || h.OCILayout.Path == "" {
		t.Fatalf("artifact did not get an OCI-layout persistence handle: %+v", h)
	}
	if err := cas.VerifyLayoutAt(h.OCILayout.Path, cas.Digest(digest)); err != nil {
		t.Fatalf("stored layout failed verification against recorded digest: %v", err)
	}

	// And the store resolves the same bytes independently.
	resolved, err := store.Resolve(cas.Digest(digest))
	if err != nil {
		t.Fatalf("store.Resolve: %v", err)
	}
	if resolved != h.OCILayout.Path {
		t.Fatalf("resolved %q != handle path %q", resolved, h.OCILayout.Path)
	}
	t.Logf("activation proof: perform retained %s to CAS, verifies on read", digest)
}
