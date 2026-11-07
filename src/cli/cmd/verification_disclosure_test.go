package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

// These tests cover the LIFECYCLE-eligibility layer the renderer tests bypass:
// "a release with evidence produces a Verification object" — and specifically that
// disclosure is EVIDENCE-driven, not gated on Tier-0. The defect they pin down:
// before this, an oidc/kms/hardware/provenance-only release produced no Verification
// at all, silently reintroducing privileged Tier-0 semantics into the human surface.

// blobSigResults is a results manifest with one successful SHA256SUMS blob signature
// carrying the given trust evidence.
func blobSigResults(ev artifact.TrustEvidence) *artifact.ResultsManifest {
	return &artifact.ResultsManifest{Results: []artifact.Result{{
		ArtifactID: "checksums:SHA256SUMS", ArtifactName: "SHA256SUMS", Kind: "checksums",
		Outcomes: []artifact.Outcome{{
			Type: artifact.OutcomeTypeBlobSignature,
			BlobSignature: &artifact.BlobSignatureOutcome{
				Status: artifact.OutcomeSuccess, Kind: "cosign",
				BlobPath: "dist/SHA256SUMS", SignaturePath: "dist/SHA256SUMS.sig",
				TrustEvidence: ev,
			},
		}},
	}}}
}

func TestBuildVerification_OIDCOnly(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{
		TrustClass: "oidc", Transparency: true, TrustDomain: "internal",
		SignerRef: "https://id.internal/oauth2/subj",
	})
	v, anchor := buildVerification(context.Background(), &config.Config{}, results, t.TempDir())
	if v == nil || v.Primary == nil {
		t.Fatal("oidc-only release must still produce a disclosure (evidence-driven, not Tier-0-gated)")
	}
	if v.Primary.Class != "oidc" || v.Primary.TrustDomain != "internal" || !v.Primary.Transparency {
		t.Fatalf("oidc dimensions missing: %+v", v.Primary)
	}
	if v.Anchor != nil || anchor != "" {
		t.Errorf("a non-Tier-0 release must NOT advertise an anchor: anchor=%q %+v", anchor, v.Anchor)
	}
}

func TestBuildVerification_KMSOnly(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{
		TrustClass: "kms", NonExportable: true, SignerRef: "release-signing-key",
	})
	v, anchor := buildVerification(context.Background(), &config.Config{}, results, t.TempDir())
	if v == nil || v.Primary == nil {
		t.Fatal("kms-only release must produce a disclosure")
	}
	if v.Primary.Class != "kms" || !v.Primary.NonExportable {
		t.Fatalf("kms dimensions missing: %+v", v.Primary)
	}
	if v.Anchor != nil || anchor != "" {
		t.Errorf("kms-only release must not advertise an anchor: anchor=%q %+v", anchor, v.Anchor)
	}
}

func TestBuildVerification_HardwareOnly(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{
		TrustClass: "hardware", PhysicalPresence: true, NonExportable: true,
	})
	v, _ := buildVerification(context.Background(), &config.Config{}, results, t.TempDir())
	if v == nil || v.Primary == nil {
		t.Fatal("hardware-only release must produce a disclosure")
	}
	if v.Primary.Class != "hardware" || !v.Primary.PhysicalPresence || !v.Primary.NonExportable {
		t.Fatalf("hardware dimensions missing: %+v", v.Primary)
	}
}

func TestBuildVerification_ProvenanceOnly(t *testing.T) {
	results := &artifact.ResultsManifest{Results: []artifact.Result{{
		ArtifactID: "docker:app", ArtifactName: "app", Kind: "docker",
		Outcomes: []artifact.Outcome{{
			Type:   artifact.OutcomeTypeProvenanceAttestation,
			Target: &artifact.OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/app"},
			ProvenanceAttestation: &artifact.ProvenanceAttestationOutcome{
				Status: artifact.OutcomeSuccess, Kind: "cosign", PredicateType: "slsaprovenance",
				VerifiedDigest: "sha256:cafe",
				TrustEvidence:  artifact.TrustEvidence{TrustClass: "kms"},
			},
		}},
	}}}
	v, anchor := buildVerification(context.Background(), &config.Config{}, results, t.TempDir())
	if v == nil {
		t.Fatal("a provenance-only release must still disclose the attestation")
	}
	if len(v.Attestations) == 0 {
		t.Errorf("provenance attestation not disclosed: %+v", v)
	}
	if v.Primary != nil {
		t.Errorf("a provenance-only release has no primary signature: %+v", v.Primary)
	}
	if anchor != "" {
		t.Errorf("no anchor expected: %q", anchor)
	}
}

// Tier-0 still discloses its tier, but the published ANCHOR is gated on a configured,
// loadable state dir — proving the two predicates (disclosure vs anchor) are decoupled.
func TestBuildVerification_Tier0DisclosesButAnchorGatedOnStateDir(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{TrustClass: "key", Tier: provision.TierSoftware})
	v, anchor := buildVerification(context.Background(), &config.Config{}, results, t.TempDir())
	if v == nil || v.Primary == nil {
		t.Fatal("tier-0 release must disclose")
	}
	if v.Primary.Tier != provision.TierSoftware {
		t.Errorf("tier-0 tier expected, got %q", v.Primary.Tier)
	}
	if v.Anchor != nil || anchor != "" {
		t.Errorf("anchor must be gated on a configured/loadable state dir: anchor=%q %+v", anchor, v.Anchor)
	}
}

func TestBuildVerification_NoEvidence(t *testing.T) {
	if v, _ := buildVerification(context.Background(), &config.Config{}, &artifact.ResultsManifest{}, t.TempDir()); v != nil {
		t.Errorf("no evidence → no Verification, got %+v", v)
	}
}

func kmsSignedResults() *artifact.ResultsManifest {
	return &artifact.ResultsManifest{Results: []artifact.Result{{
		Outcomes: []artifact.Outcome{{
			Attestation: &artifact.AttestationOutcome{
				Status: artifact.OutcomeSuccess, SignatureRef: "docker.io/a/b@sha256:1",
				TrustEvidence: artifact.TrustEvidence{TrustClass: "kms", SignerRef: "tenant"},
			},
		}},
	}}}
}

func kmsSigningConfig() *config.Config {
	return &config.Config{SigningSetup: config.SigningConfig{Profiles: []config.SigningProfile{{
		ID: "release", Requires: config.StringOrList{"kms"}, KMS: &config.KMSTrust{Ref: "tenant"},
	}}}}
}

func stubKMSExport(t *testing.T, export func(string) error) {
	t.Helper()
	t.Setenv("SF_KMS_TENANT", "hashivault://tenant")
	orig := exportKMSPublicKey
	exportKMSPublicKey = func(_ context.Context, _ string, _ map[string]config.ToolConstraint, _ sign.SignPlan, _ cosign.Env, out string) error {
		return export(out)
	}
	t.Cleanup(func() { exportKMSPublicKey = orig })
}

func TestBuildVerification_KMSReleaseAttachesExportedKey(t *testing.T) {
	stubKMSExport(t, func(out string) error { return os.WriteFile(out, []byte("pub"), 0o644) })
	v, path := buildVerification(context.Background(), kmsSigningConfig(), kmsSignedResults(), t.TempDir())
	if v == nil || v.Anchor == nil || v.Anchor.Fingerprint == "" {
		t.Fatalf("a KMS-signed release must carry its key: %+v", v)
	}
	if filepath.Base(path) != "cosign.pub" {
		t.Errorf("anchor path = %q", path)
	}
}

func TestBuildVerification_KMSExportFailureStillDiscloses(t *testing.T) {
	stubKMSExport(t, func(string) error { return errors.New("denied") })
	v, path := buildVerification(context.Background(), kmsSigningConfig(), kmsSignedResults(), t.TempDir())
	if v == nil || v.Primary == nil {
		t.Fatal("a failed export must not hide that the release is signed")
	}
	if v.Anchor != nil || path != "" {
		t.Errorf("no key may be advertised after a failed export: %q %+v", path, v.Anchor)
	}
}
