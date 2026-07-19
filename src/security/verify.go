// Package security provides the 6-layer artifact verification model.
// Layers: registry identity, signature validity, identity validity,
// provenance validity, tag-binding replay detection, shadow-write detection.
package security

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// VerifyConfidence represents the computed trust level of artifact verification.
type VerifyConfidence string

const (
	ConfidenceHigh     VerifyConfidence = "high"     // digest matched + signed + attested
	ConfidenceDegraded VerifyConfidence = "degraded" // digest matched, unsigned or partial attestation
	ConfidenceNone     VerifyConfidence = "none"     // mismatch, missing, or failed verification
)

// VerificationResult captures the outcome of multi-layer artifact verification.
type VerificationResult struct {
	// Layer 0 — Commit provenance (artifact built for the commit under review)
	ExpectedCommit string `json:"expected_commit,omitempty"`
	CurrentCommit  string `json:"current_commit,omitempty"`
	CommitMatch    *bool  `json:"commit_match,omitempty"`

	// Layer 1 — Registry identity
	ResolvedDigest string `json:"resolved_digest"`
	ExpectedDigest string `json:"expected_digest,omitempty"`
	DigestMatch    *bool  `json:"digest_match,omitempty"`

	// Layer 2 — Signature validity
	SignatureValid   *bool `json:"signature_valid,omitempty"`
	SigningAttempted bool  `json:"signing_attempted,omitempty"`

	// Layer 3 — Identity validity
	IdentityMatched *bool `json:"identity_matched,omitempty"`

	// Layer 4 — Provenance validity
	AttestationValid  *bool `json:"attestation_valid,omitempty"`
	ProvenanceMatched *bool `json:"provenance_matched,omitempty"`

	// Layer 5 — Tag-binding replay detection
	TagBindingMatch *bool    `json:"tag_binding_match,omitempty"`
	ExpectedTags    []string `json:"expected_tags,omitempty"`
	ActualTag       string   `json:"actual_tag,omitempty"`

	// Layer 6 — Shadow write / split-view detection
	ObservedConsistent *bool `json:"observed_consistent,omitempty"`

	// Computed
	Confidence VerifyConfidence `json:"confidence"`
	Failures   []string         `json:"failures,omitempty"`
}

// VerifyOpts contains the inputs for artifact verification.
type VerifyOpts struct {
	ExpectedDigest    string
	ActualRef         string
	ActualTag         string
	ObservedDigest    string // primary observation (buildx)
	ObservedDigestAlt string // alternate observation (registry API)
	ExpectedTags      []string
	ExpectedCommit    string // commit the artifact was built for (from outputs.json)
	CurrentCommit     string // commit under review (pipeline SF_CI_SHA); enables Layer 0
	SigningAttempted  bool
	Attestation       *artifact.AttestationOutcome
	CosignKeyPath     string
	CredResolver      func(string) (string, string)
	CredRef           string
	ToolchainDesired  map[string]config.ToolConstraint
}

// Verify performs 6-layer artifact verification against a digest reference.
// All verification operations use digest references (repo@sha256:...), never tags.
func Verify(ctx context.Context, opts VerifyOpts) *VerificationResult {
	r := &VerificationResult{
		ExpectedDigest:   opts.ExpectedDigest,
		SigningAttempted: opts.SigningAttempted,
	}

	// Layer 0 — Commit provenance: the artifact must have been built for the commit
	// under review. ExpectedCommit is what perform recorded (outputs.json);
	// CurrentCommit is the pipeline SHA. A mismatch means the bytes are stale or
	// foreign, so no other layer's result can be trusted — a scan of the wrong
	// artifact must never read as a pass. Checked before the digest guard so it
	// still fires when no digest observation is available.
	verifyCommitProvenance(r, opts)

	if opts.ExpectedDigest == "" {
		r.Failures = append(r.Failures, "no expected digest available")
		r.Confidence = computeConfidence(r) // Degraded by default; None if Layer 0 flagged a mismatch
		return r
	}

	// Layer 1 — Registry identity: compare expected digest to observed
	if opts.ObservedDigest != "" {
		match := opts.ExpectedDigest == opts.ObservedDigest
		r.DigestMatch = &match
		r.ResolvedDigest = opts.ObservedDigest
		if !match {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"digest mismatch: expected %s, observed %s",
				truncDigest(opts.ExpectedDigest), truncDigest(opts.ObservedDigest)))
		}
	} else {
		r.ResolvedDigest = opts.ExpectedDigest
	}

	// Layer 2 — Signature validity (cosign verify)
	verifyCosignSignature(ctx, r, opts)

	// Layer 3 — Identity validity (deferred — requires cosign cert chain)
	// Future: verify issuer + subject from cosign certificate

	// Layer 4 — Provenance validity
	verifyProvenance(r, opts)

	// Layer 5 — Tag-binding replay detection
	verifyTagBinding(r, opts)

	// Layer 6 — Shadow write / split-view detection
	verifyShadowWrite(r, opts)

	r.Confidence = computeConfidence(r)
	return r
}

// verifyCommitProvenance (Layer 0) checks the artifact was built for the commit
// under review. It records nothing when either commit is absent (nothing to
// compare — e.g. a local scan with no CI context); a present-but-different pair
// is a hard failure that computeConfidence collapses to None.
func verifyCommitProvenance(r *VerificationResult, opts VerifyOpts) {
	r.ExpectedCommit = opts.ExpectedCommit
	r.CurrentCommit = opts.CurrentCommit
	if opts.ExpectedCommit == "" || opts.CurrentCommit == "" {
		return
	}
	match := opts.ExpectedCommit == opts.CurrentCommit
	r.CommitMatch = &match
	if !match {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"commit provenance mismatch: artifact built for %s, pipeline is %s",
			truncCommit(opts.ExpectedCommit), truncCommit(opts.CurrentCommit)))
	}
}

// verifyCosignSignature runs cosign verify against a digest reference.
func verifyCosignSignature(ctx context.Context, r *VerificationResult, opts VerifyOpts) {
	rootDir, _ := os.Getwd()
	cosignVer, _ := toolchain.ResolveVersion(rootDir, "cosign", "", opts.ToolchainDesired)
	cosignResult, resolveErr := provision.Resolve(ctx, rootDir, "cosign", cosignVer, "signature verification")
	if resolveErr != nil {
		diag.Debug(false, "cosign: toolchain resolve failed, skipping signature verification: %v", resolveErr)
		if opts.SigningAttempted {
			r.Failures = append(r.Failures, "signing was configured but cosign not available for verification")
		}
		return
	}

	if opts.CosignKeyPath == "" {
		diag.Debug(false, "cosign: no key configured, skipping signature verification")
		return
	}

	digestRef := extractRepo(opts.ActualRef) + "@" + opts.ExpectedDigest
	cmd := exec.CommandContext(ctx, cosignResult.Path, "verify",
		"--key", opts.CosignKeyPath,
		"--insecure-ignore-tlog=true",
		digestRef)
	cmd.Env = toolchain.CleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		valid := false
		r.SignatureValid = &valid
		r.Failures = append(r.Failures, fmt.Sprintf("cosign verify failed: %s", strings.TrimSpace(string(out))))
		return
	}

	valid := true
	r.SignatureValid = &valid
}

// verifyProvenance checks attestation record validity.
func verifyProvenance(r *VerificationResult, opts VerifyOpts) {
	if opts.Attestation == nil {
		if opts.SigningAttempted {
			r.Failures = append(r.Failures, "signing was configured but no attestation record found")
		}
		return
	}

	// Verify the attestation covers the expected digest
	if opts.Attestation.VerifiedDigest != "" && opts.ExpectedDigest != "" {
		match := opts.Attestation.VerifiedDigest == opts.ExpectedDigest
		r.AttestationValid = &match
		if !match {
			r.Failures = append(r.Failures, fmt.Sprintf(
				"attestation digest mismatch: attested %s, expected %s",
				truncDigest(opts.Attestation.VerifiedDigest), truncDigest(opts.ExpectedDigest)))
		}
	}
}

// verifyTagBinding checks for replay/rollback attacks via tag-to-digest binding.
func verifyTagBinding(r *VerificationResult, opts VerifyOpts) {
	if len(opts.ExpectedTags) == 0 || opts.ActualTag == "" {
		return
	}

	r.ExpectedTags = opts.ExpectedTags
	r.ActualTag = opts.ActualTag

	// allowedPromotions is empty until `stagefreight promote` exists.
	// When promote lands, it will write explicit promotion records that
	// whitelist cross-tag digest reuse (e.g., dev-abc123 → v1.2.0 → latest).
	var allowedPromotions []string

	if !contains(opts.ExpectedTags, opts.ActualTag) && !contains(allowedPromotions, opts.ActualTag) {
		match := false
		r.TagBindingMatch = &match
		r.Failures = append(r.Failures, fmt.Sprintf(
			"replay detected: digest bound to tags %v, not %s",
			opts.ExpectedTags, opts.ActualTag))
	} else {
		match := true
		r.TagBindingMatch = &match
	}
}

// verifyShadowWrite checks cross-client consistency of registry responses.
func verifyShadowWrite(r *VerificationResult, opts VerifyOpts) {
	if opts.ObservedDigest == "" || opts.ObservedDigestAlt == "" {
		return
	}

	consistent := opts.ObservedDigest == opts.ObservedDigestAlt
	r.ObservedConsistent = &consistent
	if !consistent {
		r.Failures = append(r.Failures, fmt.Sprintf(
			"registry inconsistency: buildx saw %s, registry API saw %s",
			truncDigest(opts.ObservedDigest), truncDigest(opts.ObservedDigestAlt)))
	}
}

// computeConfidence determines the overall confidence from verification results.
func computeConfidence(r *VerificationResult) VerifyConfidence {
	// Hard failures → none
	if r.CommitMatch != nil && !*r.CommitMatch {
		return ConfidenceNone
	}
	if r.DigestMatch != nil && !*r.DigestMatch {
		return ConfidenceNone
	}
	if r.SignatureValid != nil && !*r.SignatureValid {
		return ConfidenceNone
	}
	if r.TagBindingMatch != nil && !*r.TagBindingMatch {
		return ConfidenceNone
	}
	if r.ObservedConsistent != nil && !*r.ObservedConsistent {
		return ConfidenceNone
	}

	// High confidence requires digest + signature + attestation
	allPresent := r.DigestMatch != nil && r.SignatureValid != nil && r.AttestationValid != nil
	allTrue := allPresent && *r.DigestMatch && *r.SignatureValid && *r.AttestationValid
	if allTrue {
		return ConfidenceHigh
	}

	return ConfidenceDegraded
}

// ConfidenceLabel returns a human-readable description for a confidence level.
func ConfidenceLabel(c VerifyConfidence) string {
	switch c {
	case ConfidenceHigh:
		return "high (digest matched + signed + attested)"
	case ConfidenceDegraded:
		return "degraded"
	case ConfidenceNone:
		return "none"
	default:
		return string(c)
	}
}

// extractRepo extracts the repository portion from an image reference (strips tag/digest).
func extractRepo(ref string) string {
	if idx := strings.LastIndex(ref, "@"); idx >= 0 {
		return ref[:idx]
	}
	if idx := strings.LastIndex(ref, ":"); idx >= 0 {
		// Make sure it's a tag separator, not a port
		slash := strings.LastIndex(ref, "/")
		if idx > slash {
			return ref[:idx]
		}
	}
	return ref
}

// extractRegistry extracts the registry hostname from an image reference.
func ExtractRegistry(ref string) string {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) < 2 {
		return ""
	}
	host := parts[0]
	if strings.Contains(host, ".") || strings.Contains(host, ":") {
		return host
	}
	return ""
}

func truncDigest(d string) string {
	if len(d) > 19 {
		return d[:19] + "..."
	}
	return d
}

func truncCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
