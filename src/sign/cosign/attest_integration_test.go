//go:build integration

package cosign

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// TestAttest_ProvenanceEndToEnd proves the provenance-attestation path end-to-end
// through the EXECUTOR: it stands up an in-process registry (ggcr serves localhost
// over HTTP, so no insecure flag is needed), pushes a real image, attests an
// extracted SLSA-provenance predicate onto its digest via cosign.Attest with an
// offline key-class plan, and confirms cosign verify-attestation accepts it against
// the public key. This is the "produced AND attached" guarantee the feature exists
// for — the predicate is cryptographically bound to the published digest, not merely
// generated. Infrastructure-free: no real registry, no Sigstore, no network.
//
//	go test -tags integration -run TestAttest_ProvenanceEndToEnd ./src/sign/cosign/
func TestAttest_ProvenanceEndToEnd(t *testing.T) {
	dir := t.TempDir()

	ver, _ := toolchain.ResolveVersion("cosign", "", nil)
	res, err := toolchain.Resolve(dir, "cosign", ver)
	if err != nil {
		t.Fatalf("resolve cosign: %v", err)
	}
	cosignBin := res.Path

	// In-process registry. ggcr uses HTTP for a "localhost" host, so reference the
	// loopback as localhost:<port> and neither push nor cosign need an insecure flag.
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	ref := "localhost:" + port + "/app"

	img, err := random.Image(1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, img); err != nil {
		t.Fatalf("push image: %v", err)
	}
	dig, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	digestRef := ref + "@" + dig.String()

	// Offline cosign keypair (empty password — the Tier-0 shape).
	gen := exec.Command(cosignBin, "generate-key-pair")
	gen.Dir = dir
	gen.Env = append(os.Environ(), "COSIGN_PASSWORD=")
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate-key-pair: %v\n%s", err, out)
	}
	keyPath := filepath.Join(dir, "cosign.key")
	pubPath := filepath.Join(dir, "cosign.pub")

	// A predicate BODY (what cosign attest --predicate expects — cosign frames it
	// with the image subject), mirroring the SLSA predicate StageFreight extracts
	// from its in-toto provenance statement.
	predPath := filepath.Join(dir, "provenance.predicate.json")
	pred := map[string]any{
		"buildType": "https://stagefreight.dev/build/docker/v1",
		"builder":   map[string]any{"id": "pkg:docker/stagefreight/image"},
	}
	pb, _ := json.Marshal(pred)
	if err := os.WriteFile(predPath, pb, 0o644); err != nil {
		t.Fatal(err)
	}

	// Attest via the executor: key class, offline (no transparency).
	plan := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: keyPath}
	opts := sign.SignOptions{PredicatePath: predPath, PredicateType: "slsaprovenance"}
	if err := Attest(context.Background(), dir, nil, digestRef, plan, EnvForPlan(plan), opts); err != nil {
		t.Fatalf("Attest provenance onto %s: %v", digestRef, err)
	}

	// The attestation must verify against the public key.
	verify := exec.Command(cosignBin, "verify-attestation",
		"--key", pubPath, "--type", "slsaprovenance", "--insecure-ignore-tlog=true", digestRef)
	verify.Env = os.Environ()
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("verify-attestation failed — provenance was not authentically bound: %v\n%s", err, out)
	}
}
