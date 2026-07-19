package security

import "testing"

// TestVerifyCommitProvenance pins Layer 0: the artifact must have been built for
// the commit under review. A present-but-different pair is a hard failure that
// collapses confidence to None; an absent pairing no-ops (local scans with no CI
// context keep working exactly as before).
func TestVerifyCommitProvenance(t *testing.T) {
	t.Run("mismatch -> CommitMatch false, confidence None", func(t *testing.T) {
		r := &VerificationResult{}
		verifyCommitProvenance(r, VerifyOpts{ExpectedCommit: "commit-A", CurrentCommit: "commit-B"})
		if r.CommitMatch == nil || *r.CommitMatch {
			t.Fatal("CommitMatch must be present and false on a mismatch")
		}
		if len(r.Failures) == 0 {
			t.Fatal("a commit mismatch must record a failure")
		}
		if got := computeConfidence(r); got != ConfidenceNone {
			t.Fatalf("confidence = %q, want none — a scan of the wrong-commit artifact must never pass", got)
		}
	})

	t.Run("match -> CommitMatch true, Layer 0 does not force None", func(t *testing.T) {
		r := &VerificationResult{}
		verifyCommitProvenance(r, VerifyOpts{ExpectedCommit: "commit-A", CurrentCommit: "commit-A"})
		if r.CommitMatch == nil || !*r.CommitMatch {
			t.Fatal("CommitMatch must be present and true on a match")
		}
		if got := computeConfidence(r); got == ConfidenceNone {
			t.Fatalf("matching commit must not force confidence None (got %q)", got)
		}
	})

	t.Run("absent CurrentCommit -> Layer 0 no-ops (back-compat)", func(t *testing.T) {
		r := &VerificationResult{}
		verifyCommitProvenance(r, VerifyOpts{ExpectedCommit: "commit-A"}) // no CurrentCommit
		if r.CommitMatch != nil {
			t.Fatal("CommitMatch must stay nil when there is no CI commit to compare")
		}
		if got := computeConfidence(r); got == ConfidenceNone {
			t.Fatalf("absent commit pairing must not force None (got %q)", got)
		}
	})
}
