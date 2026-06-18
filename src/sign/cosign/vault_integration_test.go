//go:build integration

package cosign

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// TestSignBlob_VaultTransit proves the kms class end-to-end against a REAL Vault
// transit backend: StageFreight resolves a kms profile to hashivault://<key> via
// EnvForPlan, signs SHA256SUMS through the executor (the renderer's offline kms
// flags), and cosign verify-blob confirms it against the Vault key. This validates
// Vault as an operator-provided signer (no custodianship): the test stands up a dev
// Vault only to exercise the consumption path. Skips if vault is not installed.
//
//	go test -tags integration -run TestSignBlob_VaultTransit ./src/sign/cosign/
func TestSignBlob_VaultTransit(t *testing.T) {
	if _, err := exec.LookPath("vault"); err != nil {
		t.Skip("vault binary not available")
	}
	dir := t.TempDir()

	ver, _ := toolchain.ResolveVersion("cosign", "", nil)
	res, err := toolchain.Resolve(dir, "cosign", ver)
	if err != nil {
		t.Fatalf("resolve cosign: %v", err)
	}
	cosignBin := res.Path

	const addr = "http://127.0.0.1:8209"
	vault := exec.Command("vault", "server", "-dev", "-dev-root-token-id=root", "-dev-listen-address=127.0.0.1:8209")
	vault.Env = append(os.Environ(), "VAULT_ADDR="+addr)
	if err := vault.Start(); err != nil {
		t.Fatalf("start dev vault: %v", err)
	}
	defer func() { _ = vault.Process.Kill() }()
	t.Setenv("VAULT_ADDR", addr)
	t.Setenv("VAULT_TOKEN", "root")
	time.Sleep(3 * time.Second)

	vrun := func(args ...string) {
		c := exec.Command("vault", args...)
		c.Env = os.Environ()
		_ = c.Run()
	}
	vrun("secrets", "enable", "transit")
	vrun("write", "-f", "transit/keys/sfblob", "type=ecdsa-p256")

	// A kms profile binds to hashivault://sfblob (key name only — cosign prepends
	// transit/keys/). EnvForPlan resolves the ref into the Env witness.
	t.Setenv("SF_KMS_SFBLOB", "hashivault://sfblob")
	plan := sign.SignPlan{TrustClass: sign.ClassKMS, KMSRef: "sfblob"}
	env := EnvForPlan(plan)
	if len(env.KMS) != 1 || env.KMS[0].URI != "hashivault://sfblob" {
		t.Fatalf("kms URI did not bind into Env: %+v", env.KMS)
	}

	blob := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(blob, []byte("deadbeef  release.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := blob + ".sig"
	if err := SignBlob(context.Background(), dir, nil, blob, sig, plan, env); err != nil {
		t.Fatalf("SignBlob via Vault transit: %v", err)
	}
	if _, err := os.Stat(sig); err != nil {
		t.Fatalf("no signature produced: %v", err)
	}

	verify := exec.Command(cosignBin, "verify-blob", "--key", "hashivault://sfblob",
		"--signature", sig, "--insecure-ignore-tlog=true", blob)
	verify.Env = os.Environ() // VAULT_ADDR / VAULT_TOKEN
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("cosign verify-blob against the Vault key failed: %v\n%s", err, out)
	}
}
