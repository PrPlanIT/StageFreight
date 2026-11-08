//go:build integration

package cosign

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// TestSignBlobLoggedInPublicSigstore proves a logged signature the way a release makes one:
// the executor resolves public Sigstore's published config through TUF, cosign records the
// signature in its Rekor v2 log with a timestamp, and the bundle verifies with the log
// required. It writes one entry to the public log, so it is gated behind -tags integration.
//
//	go test -tags integration -run TestSignBlobLoggedInPublicSigstore ./src/sign/cosign/
func TestSignBlobLoggedInPublicSigstore(t *testing.T) {
	dir := t.TempDir()
	ver, _ := toolchain.ResolveVersion("", "cosign", "", nil)
	res, err := toolchain.Resolve(dir, "cosign", ver)
	if err != nil {
		t.Fatalf("resolve cosign: %v", err)
	}
	gen := exec.Command(res.Path, "generate-key-pair")
	gen.Dir = dir
	gen.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate-key-pair: %v\n%s", err, out)
	}
	blob := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(blob, []byte("deadbeef  release-v1.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COSIGN_PASSWORD", "")
	t.Setenv("COSIGN_KEY", filepath.Join(dir, "cosign.key"))
	plan := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:COSIGN_KEY", TransparencyRequired: true}

	bundle := blob + ".sigstore.json"
	if err := SignBlob(context.Background(), dir, nil, blob, bundle, plan, EnvForPlan(plan)); err != nil {
		t.Fatalf("SignBlob: %v", err)
	}
	data, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		VerificationMaterial struct {
			TlogEntries []struct {
				KindVersion struct{ Version string } `json:"kindVersion"`
			} `json:"tlogEntries"`
			TimestampVerificationData struct {
				RFC3161Timestamps []json.RawMessage `json:"rfc3161Timestamps"`
			} `json:"timestampVerificationData"`
		} `json:"verificationMaterial"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	if e := b.VerificationMaterial.TlogEntries; len(e) != 1 || e[0].KindVersion.Version != "0.0.2" {
		t.Errorf("want one Rekor v2 (hashedrekord 0.0.2) entry: %+v", e)
	}
	if len(b.VerificationMaterial.TimestampVerificationData.RFC3161Timestamps) != 1 {
		t.Error("a Rekor v2 entry needs its timestamp authority's timestamp")
	}

	verify := exec.Command(res.Path, "verify-blob", "--key", filepath.Join(dir, "cosign.pub"), "--bundle", bundle, blob)
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("verify-blob with the log required: %v\n%s", err, out)
	}
}
