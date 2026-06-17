package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// appendOutcome must LAYER assurance onto immutable outputs: append, never replace,
// so a stronger signature added later preserves the lower-tier one.
func TestAppendOutcome_IsAdditive(t *testing.T) {
	results := &artifact.ResultsManifest{Results: []artifact.Result{{
		ArtifactID: "checksums:SHA256SUMS", ArtifactName: "SHA256SUMS", Kind: "checksums",
		Outcomes: []artifact.Outcome{{
			Type:          artifact.OutcomeTypeBlobSignature,
			BlobSignature: &artifact.BlobSignatureOutcome{Status: artifact.OutcomeSuccess, TrustEvidence: artifact.TrustEvidence{Tier: "tier0-software"}},
		}},
	}}}

	appendOutcome(results, "checksums:SHA256SUMS", "SHA256SUMS", "checksums", artifact.Outcome{
		Type:          artifact.OutcomeTypeBlobSignature,
		BlobSignature: &artifact.BlobSignatureOutcome{Status: artifact.OutcomeSuccess, TrustEvidence: artifact.TrustEvidence{TrustClass: "hardware", PhysicalPresence: true}},
	})

	got := results.Results[0].Outcomes
	if len(got) != 2 {
		t.Fatalf("expected 2 outcomes (additive), got %d", len(got))
	}
	if got[0].BlobSignature.Tier != "tier0-software" {
		t.Error("the lower-tier signature was replaced — layering broken")
	}
	if got[1].BlobSignature.TrustClass != "hardware" || !got[1].BlobSignature.PhysicalPresence {
		t.Error("the new hardware signature was not appended")
	}

	// A previously-unsigned artifact gets a new Result.
	appendOutcome(results, "checksums:OTHER", "OTHER", "checksums", artifact.Outcome{
		Type:          artifact.OutcomeTypeBlobSignature,
		BlobSignature: &artifact.BlobSignatureOutcome{Status: artifact.OutcomeSuccess},
	})
	if len(results.Results) != 2 {
		t.Errorf("a new artifact should add a Result, got %d", len(results.Results))
	}
}
