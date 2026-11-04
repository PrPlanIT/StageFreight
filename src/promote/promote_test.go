package promote

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/internal/testdocker"
)

// TestLayoutToRegistry_PreservesDigest is the P4 promotion proof: an OCI layout
// pushed via go-containerregistry arrives at the registry under the SAME index
// digest the layout recorded — no daemon round-trip, no rebuild, identity
// preserved. This is the empirical guarantee that "publish distributes exactly
// what review approved."
//
// Docker-gated: builds a layout with buildx and runs a local registry.
func TestLayoutToRegistry_PreservesDigest(t *testing.T) {
	if !testdocker.Available() {
		t.Skip("docker not available — promote integration test runs only on a docker-capable runner")
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM docker.io/library/alpine:3.24.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layoutDir := filepath.Join(t.TempDir(), "layout")
	metaFile := filepath.Join(t.TempDir(), "m.json")

	// Build an OCI layout directory + capture the recorded digest.
	build := exec.Command("docker", "buildx", "build",
		"--output", "type=oci,tar=false,dest="+layoutDir,
		"--metadata-file", metaFile, work)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("buildx build: %v\n%s", err, out)
	}
	var meta struct {
		Digest string `json:"containerimage.digest"`
	}
	data, err := os.ReadFile(metaFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Digest == "" {
		t.Fatal("no containerimage.digest in metadata")
	}

	reg := testdocker.Registry(t)
	ref := reg + "/promote-test:v1"
	res, err := LayoutToRegistry(context.Background(), layoutDir, ref, meta.Digest, nil)
	if err != nil {
		t.Fatalf("LayoutToRegistry: %v", err)
	}

	if res.Digest != meta.Digest {
		t.Fatalf("DIGEST NOT PRESERVED: recorded %s, registry served %s", meta.Digest, res.Digest)
	}
	if !strings.HasPrefix(res.Ref, reg+"/promote-test") {
		t.Fatalf("unexpected pushed ref %q", res.Ref)
	}
	t.Logf("promotion proof: layout digest %s preserved through registry push", res.Digest)
}

// TestLayoutToRegistry_RejectsDigestMismatch proves the pre-push guard: if the
// caller's recorded digest does not match the layout's actual index digest,
// promotion refuses rather than distributing a different artifact than was
// reviewed. This does not need a registry — it fails before any push.
func TestLayoutToRegistry_RejectsDigestMismatch(t *testing.T) {
	if !testdocker.Available() {
		t.Skip("docker not available")
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM docker.io/library/alpine:3.24.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layoutDir := filepath.Join(t.TempDir(), "layout")
	build := exec.Command("docker", "buildx", "build", "--output", "type=oci,tar=false,dest="+layoutDir, work)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("buildx build: %v\n%s", err, out)
	}

	const wrong = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	_, err := LayoutToRegistry(context.Background(), layoutDir, "localhost:5971/x:v1", wrong, nil)
	if err == nil {
		t.Fatal("promotion accepted a layout whose digest != recorded digest — must refuse")
	}
	if !strings.Contains(err.Error(), "refusing to distribute") && !strings.Contains(err.Error(), "not an entry") {
		t.Fatalf("expected refusal error, got: %v", err)
	}
}
