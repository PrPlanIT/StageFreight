package promote

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dockerAvailable reports whether docker + daemon are usable. The promote
// integration test needs docker to build a layout and run a throwaway registry;
// it skips in the pure golang test suite.
func dockerAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return exec.Command("docker", "info").Run() == nil
}

// TestLayoutToRegistry_PreservesDigest is the P4 promotion proof: an OCI layout
// pushed via go-containerregistry arrives at the registry under the SAME index
// digest the layout recorded — no daemon round-trip, no rebuild, identity
// preserved. This is the empirical guarantee that "publish distributes exactly
// what review approved."
//
// Docker-gated: builds a layout with buildx and runs a local registry.
func TestLayoutToRegistry_PreservesDigest(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker not available — promote integration test runs only on a docker-capable runner")
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM alpine:3.19\n"), 0o644); err != nil {
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

	// Throwaway local registry.
	reg := exec.Command("docker", "run", "-d", "--rm", "-p", "5970:5000", "--name", "sf-promote-test-reg", "registry:2")
	if out, err := reg.CombinedOutput(); err != nil {
		t.Fatalf("start registry: %v\n%s", err, out)
	}
	defer exec.Command("docker", "rm", "-f", "sf-promote-test-reg").Run()
	// Give the registry a moment.
	exec.Command("sh", "-c", "sleep 2").Run()

	ref := "localhost:5970/promote-test:v1"
	res, err := LayoutToRegistry(context.Background(), layoutDir, ref, meta.Digest, nil)
	if err != nil {
		t.Fatalf("LayoutToRegistry: %v", err)
	}

	if res.Digest != meta.Digest {
		t.Fatalf("DIGEST NOT PRESERVED: recorded %s, registry served %s", meta.Digest, res.Digest)
	}
	if !strings.HasPrefix(res.Ref, "localhost:5970/promote-test") {
		t.Fatalf("unexpected pushed ref %q", res.Ref)
	}
	t.Logf("promotion proof: layout digest %s preserved through registry push", res.Digest)
}

// TestLayoutToRegistry_RejectsDigestMismatch proves the pre-push guard: if the
// caller's recorded digest does not match the layout's actual index digest,
// promotion refuses rather than distributing a different artifact than was
// reviewed. This does not need a registry — it fails before any push.
func TestLayoutToRegistry_RejectsDigestMismatch(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker not available")
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"), []byte("FROM alpine:3.19\n"), 0o644); err != nil {
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
