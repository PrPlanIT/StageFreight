package security

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// Layer 2 reads the signature recorded when the image was signed and verified: a verified
// signature over the expected digest reaches high confidence, one that did not verify fails
// verification, and a configured signature with no record is reported.
func TestVerify_RecordedSignature(t *testing.T) {
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := VerifyOpts{ExpectedDigest: digest, ObservedDigest: digest, ActualRef: "cr.optcp.com/org/app@" + digest}

	verified := base
	verified.SigningAttempted = true
	verified.Attestation = &artifact.AttestationOutcome{Status: artifact.OutcomeSuccess, VerifiedDigest: digest}
	if r := Verify(verified); r.Confidence != ConfidenceHigh || r.SignatureValid == nil || !*r.SignatureValid || len(r.Failures) != 0 {
		t.Errorf("a verified signature over the expected digest: %+v", r)
	}

	unverified := base
	unverified.SigningAttempted = true
	unverified.Attestation = &artifact.AttestationOutcome{Status: artifact.OutcomeFailed, VerifiedDigest: digest,
		Error: "signature made, but it does not verify: cosign verify: no matching signatures"}
	r := Verify(unverified)
	if r.Confidence != ConfidenceNone || r.SignatureValid == nil || *r.SignatureValid || r.AttestationValid != nil {
		t.Errorf("a signature that did not verify must fail verification: %+v", r)
	}
	if len(r.Failures) != 1 || !strings.Contains(r.Failures[0], "no matching signatures") {
		t.Errorf("the recorded reason must be reported once: %v", r.Failures)
	}

	missing := base
	missing.SigningAttempted = true
	if r := Verify(missing); r.SignatureValid != nil || len(r.Failures) != 1 || !strings.Contains(r.Failures[0], "no signature was recorded") {
		t.Errorf("configured signing with no record: %+v", r)
	}

	if r := Verify(base); r.SignatureValid != nil || len(r.Failures) != 0 || r.Confidence != ConfidenceDegraded {
		t.Errorf("an image not yet signed is degraded, not failed: %+v", r)
	}
}
