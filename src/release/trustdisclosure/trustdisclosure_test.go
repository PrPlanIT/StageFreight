package trustdisclosure

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

func blobSig(ev artifact.TrustEvidence, sigName string) artifact.Outcome {
	return artifact.Outcome{
		Type: artifact.OutcomeTypeBlobSignature,
		BlobSignature: &artifact.BlobSignatureOutcome{
			Status: artifact.OutcomeSuccess, Kind: "cosign",
			BlobPath: "dist/SHA256SUMS", SignaturePath: "dist/" + sigName, TrustEvidence: ev,
		},
	}
}

func results(outcomes ...artifact.Outcome) *artifact.ResultsManifest {
	return &artifact.ResultsManifest{Results: []artifact.Result{{
		ArtifactID: "checksums:SHA256SUMS", ArtifactName: "SHA256SUMS", Kind: "checksums", Outcomes: outcomes,
	}}}
}

// Tier-0 is the disclosure primary even when it appears AFTER another signature —
// the anchor is the headline when present, and ChecksumSig points at its blob.
func TestBuild_Tier0SortsToPrimary(t *testing.T) {
	r := results(
		blobSig(artifact.TrustEvidence{TrustClass: "hardware", NonExportable: true}, "SHA256SUMS.maintainer.sig"),
		blobSig(artifact.TrustEvidence{TrustClass: "key", Tier: provision.TierSoftware}, "SHA256SUMS.sig"),
	)
	d := Build(r, nil)
	if d == nil || d.Primary == nil {
		t.Fatal("expected a disclosure with a primary")
	}
	if d.Primary.Tier != provision.TierSoftware {
		t.Errorf("Tier-0 must be the primary regardless of order, got %+v", d.Primary)
	}
	if len(d.Layers) != 1 || d.Layers[0].Class != "hardware" {
		t.Errorf("the non-primary signature must be a layer: %+v", d.Layers)
	}
	if d.ChecksumSig() != "SHA256SUMS.sig" {
		t.Errorf("ChecksumSig must be the Tier-0 blob sig, got %q", d.ChecksumSig())
	}
}

// The anchor is attached ONLY when this release actually carries a Tier-0 signature —
// passing an anchor for a non-Tier-0 release must not advertise it.
func TestBuild_AnchorGatedOnTier0(t *testing.T) {
	anchor := &Anchor{Fingerprint: "sha256:abc", Asset: "cosign.pub"}

	nonTier0 := Build(results(blobSig(artifact.TrustEvidence{TrustClass: "kms"}, "SHA256SUMS.sig")), anchor)
	if nonTier0.Anchor != nil {
		t.Errorf("a non-Tier-0 release must not attach the anchor, got %+v", nonTier0.Anchor)
	}

	tier0 := Build(results(blobSig(artifact.TrustEvidence{TrustClass: "key", Tier: provision.TierSoftware}, "SHA256SUMS.sig")), anchor)
	if tier0.Anchor == nil || tier0.Anchor.Fingerprint != "sha256:abc" {
		t.Errorf("a Tier-0 release must attach the passed anchor, got %+v", tier0.Anchor)
	}
}

// Identical signatures collapse to one (the primary), with no duplicate layer.
func TestBuild_LayerDedup(t *testing.T) {
	ev := artifact.TrustEvidence{TrustClass: "kms", NonExportable: true}
	d := Build(results(blobSig(ev, "SHA256SUMS.sig"), blobSig(ev, "SHA256SUMS.sig")), nil)
	if d.Primary == nil {
		t.Fatal("expected a primary")
	}
	if len(d.Layers) != 0 {
		t.Errorf("identical signatures must dedup to just the primary, got layers %+v", d.Layers)
	}
}

func TestBuild_NoEvidence(t *testing.T) {
	if d := Build(&artifact.ResultsManifest{}, nil); d != nil {
		t.Errorf("no evidence → nil disclosure, got %+v", d)
	}
	if d := Build(nil, nil); d != nil {
		t.Errorf("nil results → nil disclosure, got %+v", d)
	}
}
