package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/config"
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
	v, anchor := buildVerification(config.SigningConfig{}, results, t.TempDir())
	if v == nil {
		t.Fatal("oidc-only release must still produce a Verification (evidence-driven, not Tier-0-gated)")
	}
	if v.TrustClass != "oidc" || v.TrustDomain != "internal" || !v.Transparency {
		t.Fatalf("oidc dimensions missing: %+v", v)
	}
	if v.TierLabel != "keyless (OIDC identity)" {
		t.Errorf("expected a class-based tier label, got %q", v.TierLabel)
	}
	if v.Fingerprint != "" || v.AnchorAsset != "" || v.Continuity || anchor != "" {
		t.Errorf("a non-Tier-0 release must NOT advertise an anchor: %+v anchor=%q", v, anchor)
	}
}

func TestBuildVerification_KMSOnly(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{
		TrustClass: "kms", NonExportable: true, SignerRef: "release-signing-key",
	})
	v, anchor := buildVerification(config.SigningConfig{}, results, t.TempDir())
	if v == nil {
		t.Fatal("kms-only release must produce a Verification")
	}
	if v.TrustClass != "kms" || !v.NonExportable {
		t.Fatalf("kms dimensions missing: %+v", v)
	}
	if v.AnchorAsset != "" || anchor != "" {
		t.Errorf("kms-only release must not advertise an anchor: %+v anchor=%q", v, anchor)
	}
}

func TestBuildVerification_HardwareOnly(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{
		TrustClass: "hardware", PhysicalPresence: true, NonExportable: true,
	})
	v, _ := buildVerification(config.SigningConfig{}, results, t.TempDir())
	if v == nil {
		t.Fatal("hardware-only release must produce a Verification")
	}
	if v.TrustClass != "hardware" || !v.PhysicalPresence || !v.NonExportable {
		t.Fatalf("hardware dimensions missing: %+v", v)
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
	v, anchor := buildVerification(config.SigningConfig{}, results, t.TempDir())
	if v == nil {
		t.Fatal("a provenance-only release must still disclose the attestation")
	}
	if len(v.ProvenanceAttestations) == 0 {
		t.Errorf("provenance attestation not disclosed: %+v", v)
	}
	if anchor != "" {
		t.Errorf("no anchor expected: %q", anchor)
	}
}

// Tier-0 still discloses its tier, but the published ANCHOR is gated on a configured,
// loadable state dir — proving the two predicates (disclosure vs anchor) are decoupled.
func TestBuildVerification_Tier0DisclosesButAnchorGatedOnStateDir(t *testing.T) {
	results := blobSigResults(artifact.TrustEvidence{TrustClass: "key", Tier: provision.TierSoftware})
	v, anchor := buildVerification(config.SigningConfig{}, results, t.TempDir())
	if v == nil {
		t.Fatal("tier-0 release must disclose")
	}
	if v.TierLabel != "Tier-0 (persistent software key)" {
		t.Errorf("tier-0 label expected, got %q", v.TierLabel)
	}
	if v.AnchorAsset != "" || anchor != "" {
		t.Errorf("anchor must be gated on a configured/loadable state dir: %+v anchor=%q", v, anchor)
	}
}

func TestBuildVerification_NoEvidence(t *testing.T) {
	if v, _ := buildVerification(config.SigningConfig{}, &artifact.ResultsManifest{}, t.TempDir()); v != nil {
		t.Errorf("no evidence → no Verification, got %+v", v)
	}
}
