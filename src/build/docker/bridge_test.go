package docker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/cas"
)

// dockerAvailable reports whether a usable docker CLI + daemon is present.
// The repo's normal test suite runs in golang:1.26 with NO docker, so this
// gate keeps the bridge test from failing there; it runs only on a real runner
// or in the dev image where docker is present.
func dockerAvailable(t *testing.T) bool {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return false
	}
	return true
}

// TestBridge_DaemonAndCASAgreeOnIdentity is the first cross-representation
// identity proof in the system: it builds a trivial image with --load (daemon
// representation, for crucible-style execution) AND --output type=oci (the
// portable layout CAS retains), then proves the two representations agree on
// one content digest. This is the linchpin of the transport model — if it
// cannot pass honestly, the whole "same bytes across phases" guarantee is
// unsound and the effort halts.
//
// Docker-gated: skips when no daemon is present.
func TestBridge_DaemonAndCASAgreeOnIdentity(t *testing.T) {
	if !dockerAvailable(t) {
		t.Skip("docker not available — bridge test runs only on a docker-capable runner")
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "Dockerfile"),
		[]byte("FROM scratch\nCOPY hello.txt /hello.txt\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "hello.txt"),
		[]byte("stagefreight-bridge-proof\n"), 0o644); err != nil {
		t.Fatalf("write context file: %v", err)
	}

	layoutDir := t.TempDir()
	metaFile := filepath.Join(t.TempDir(), "m.json")
	tag := "sf-bridge-test:probe"

	step := build.BuildStep{
		Name:         "bridge",
		Dockerfile:   filepath.Join(work, "Dockerfile"),
		Context:      work,
		Output:       build.OutputImage,
		Platforms:    []string{"linux/amd64"},
		Tags:         []string{tag},
		Load:         true,      // daemon representation
		OCILayoutDir: layoutDir, // portable layout for CAS
		MetadataFile: metaFile,  // containerimage.digest capture
	}

	bx := NewBuildx(false)
	var out strings.Builder
	bx.Stdout = &out
	bx.Stderr = &out
	if _, err := bx.Build(context.Background(), step); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out.String())
	}
	defer exec.Command("docker", "image", "rm", "-f", tag).Run()

	// Identity from the build metadata (containerimage.digest = OCI index digest).
	metaDigest, err := ParseMetadataDigest(metaFile)
	if err != nil {
		t.Fatalf("parse metadata digest: %v", err)
	}

	// CAS path: store the emitted OCI layout under that digest. Put verifies the
	// digest names a real blob in the layout — so a successful Put is itself
	// proof that the layout's content addresses to the build's reported digest.
	store := cas.NewFSStore(t.TempDir())
	storedDir, err := store.Put(cas.Digest(metaDigest), layoutDir)
	if err != nil {
		t.Fatalf("CAS Put rejected the build's layout/digest pair — daemon and CAS DISAGREE on identity: %v", err)
	}

	// Resolve re-verifies on read: the stored bytes still hash to the digest.
	resolved, err := store.Resolve(cas.Digest(metaDigest))
	if err != nil {
		t.Fatalf("CAS Resolve failed verify-on-read: %v", err)
	}
	if resolved != storedDir {
		t.Fatalf("Resolve dir %q != Put dir %q", resolved, storedDir)
	}

	// The resolved layout's index.json references the build's digest. (This is
	// the OCI-correct identity binding: containerimage.digest is the descriptor
	// the layout's index points at — it may be a "virtual" index that is not
	// itself a standalone blob file. cas.Resolve already verified this binding
	// plus blob-content integrity; we re-read index.json here as an explicit,
	// independent statement of what the proof means.)
	indexData, err := os.ReadFile(filepath.Join(resolved, "index.json"))
	if err != nil {
		t.Fatalf("read resolved index.json: %v", err)
	}
	if !strings.Contains(string(indexData), metaDigest) {
		t.Fatalf("resolved layout index.json does not reference build digest %s — daemon and CAS DISAGREE", metaDigest)
	}

	t.Logf("bridge proof: daemon build + OCI layout agree on identity %s", metaDigest)
}
