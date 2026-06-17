//go:build integration

package cosign

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// TestSignBlobEndToEnd proves the key-class blob path the way a release uses it:
// resolve cosign via the toolchain, generate an ephemeral keypair, sign a blob
// through the executor, and cosign verify-blob the result. Gated behind
// `-tags integration` because it downloads cosign and is not a unit test.
//
//	go test -tags integration -run TestSignBlobEndToEnd ./src/sign/cosign/
func TestSignBlobEndToEnd(t *testing.T) {
	dir := t.TempDir()

	ver, _ := toolchain.ResolveVersion("cosign", "", nil)
	res, err := toolchain.Resolve(dir, "cosign", ver)
	if err != nil {
		t.Fatalf("resolve cosign: %v", err)
	}
	cosignBin := res.Path

	// Ephemeral keypair with an empty password.
	t.Setenv("COSIGN_PASSWORD", "")
	gen := exec.Command(cosignBin, "generate-key-pair")
	gen.Dir = dir
	gen.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate-key-pair: %v\n%s", err, out)
	}
	keyPath := filepath.Join(dir, "cosign.key")
	pubPath := filepath.Join(dir, "cosign.pub")

	blob := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(blob, []byte("deadbeef  release-v1.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Key-class plan referencing the generated key (legacy-style ref).
	t.Setenv("COSIGN_KEY", keyPath)
	keyPlan := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:COSIGN_KEY"}
	if !sign.Enabled(keyPlan) {
		t.Fatal("key plan should be enabled with COSIGN_KEY pointing at the generated key")
	}

	sigPath, err := SignBlob(context.Background(), dir, nil, blob, keyPlan, Env{})
	if err != nil {
		t.Fatalf("SignBlob: %v", err)
	}
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("no detached signature produced: %v", err)
	}

	verify := exec.Command(cosignBin, "verify-blob",
		"--key", pubPath, "--signature", sigPath, "--insecure-ignore-tlog=true", blob)
	verify.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("cosign verify-blob failed — the stamp does not verify: %v\n%s", err, out)
	}
}
