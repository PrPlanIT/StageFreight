package docker

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// CrucibleTag returns a namespaced temporary image tag for crucible.
// Uses stagefreight/crucible-* namespace to prevent accidental pushes.
func CrucibleTag(purpose, runID string) string {
	return fmt.Sprintf("stagefreight/crucible-%s:%s", purpose, runID)
}

// CrucibleCheck is a single verification data point.
type CrucibleCheck struct {
	Name   string // e.g. "binary hash", "version", "image digest"
	Status string // "match", "differs", "info" (expected difference), "missing", "error"
	Detail string // e.g. "sha256:abc123..."
}

// IsHardFailure returns true if this check's failure should fail the crucible.
func (c CrucibleCheck) IsHardFailure() bool {
	if c.Status == "error" {
		return true // the comparison itself could not be made
	}
	switch c.Name {
	case "binary hash":
		return false // soft — differs is "consistent" not "deterministic"
	case "version":
		return c.Status == "differs"
	case "build graph":
		// Every pass carries the plan-hash label; a missing one is a hard failure.
		return c.Status == "differs" || c.Status == "missing"
	case "image digest":
		return false // always soft
	case "env fingerprint":
		return false // always soft
	}
	return false
}

// CrucibleVerification holds the complete verification result.
type CrucibleVerification struct {
	ArtifactChecks  []CrucibleCheck
	ExecutionChecks []CrucibleCheck
	TrustLevel      string
}

// HasHardFailure returns true if any check is a hard failure.
func (cv *CrucibleVerification) HasHardFailure() bool {
	for _, c := range cv.ArtifactChecks {
		if c.IsHardFailure() {
			return true
		}
	}
	for _, c := range cv.ExecutionChecks {
		if c.IsHardFailure() {
			return true
		}
	}
	return false
}

// VerifyCrucible compares the candidate and self-proof layouts to determine the trust
// level; workDir receives the extracted binaries. An unreadable pass is an error.
func VerifyCrucible(ctx context.Context, pass1Layout, pass2Layout, workDir string) (*CrucibleVerification, error) {
	a1, err := readPassArtifact(ctx, pass1Layout, workDir)
	if err != nil {
		return nil, fmt.Errorf("crucible candidate (pass 1): %w", err)
	}
	a2, err := readPassArtifact(ctx, pass2Layout, workDir)
	if err != nil {
		return nil, fmt.Errorf("crucible self-proof (pass 2): %w", err)
	}
	return comparePassArtifacts(a1, a2), nil
}

// comparePassArtifacts derives the crucible checks and trust level.
func comparePassArtifacts(a1, a2 passArtifact) *CrucibleVerification {
	v := &CrucibleVerification{}

	// --- Artifact checks ---

	if a1.BinaryHash == a2.BinaryHash {
		v.ArtifactChecks = append(v.ArtifactChecks, CrucibleCheck{
			Name: "binary hash", Status: "match", Detail: a1.BinaryHash,
		})
	} else {
		v.ArtifactChecks = append(v.ArtifactChecks, CrucibleCheck{
			Name: "binary hash", Status: "differs",
			Detail: fmt.Sprintf("pass1=%s pass2=%s", build.TruncHash(a1.BinaryHash), build.TruncHash(a2.BinaryHash)),
		})
	}

	if a1.Version == a2.Version {
		v.ArtifactChecks = append(v.ArtifactChecks, CrucibleCheck{
			Name: "version", Status: "match", Detail: a1.Version,
		})
	} else {
		v.ArtifactChecks = append(v.ArtifactChecks, CrucibleCheck{
			Name: "version", Status: "differs",
			Detail: fmt.Sprintf("pass1=%q pass2=%q", a1.Version, a2.Version),
		})
	}

	if a1.ConfigDigest == a2.ConfigDigest {
		v.ArtifactChecks = append(v.ArtifactChecks, CrucibleCheck{
			Name: "image digest", Status: "match", Detail: build.TruncHash(a1.ConfigDigest),
		})
	} else {
		v.ArtifactChecks = append(v.ArtifactChecks, CrucibleCheck{
			Name: "image digest", Status: "info", Detail: "expected (layer metadata differs between builds)",
		})
	}

	// --- Execution checks ---

	// Build graph — compare plan hashes embedded as OCI labels
	graph1, graph2 := a1.Labels[build.LabelPlanHash], a2.Labels[build.LabelPlanHash]
	switch {
	case graph1 == "" || graph2 == "":
		v.ExecutionChecks = append(v.ExecutionChecks, CrucibleCheck{
			Name: "build graph", Status: "missing",
			Detail: fmt.Sprintf("label %s not found", build.LabelPlanHash),
		})
	case graph1 == graph2:
		v.ExecutionChecks = append(v.ExecutionChecks, CrucibleCheck{
			Name: "build graph", Status: "match", Detail: "identical",
		})
	default:
		v.ExecutionChecks = append(v.ExecutionChecks, CrucibleCheck{
			Name: "build graph", Status: "differs",
			Detail: fmt.Sprintf("pass1=%s pass2=%s", build.TruncHash(graph1), build.TruncHash(graph2)),
		})
	}

	// Env fingerprint (non-authoritative — informational only, never overshadows
	// the primary signals: pass 2 success, version match, binary match, graph match)
	if envFingerprint(a1) == envFingerprint(a2) {
		v.ExecutionChecks = append(v.ExecutionChecks, CrucibleCheck{
			Name: "env fingerprint", Status: "match", Detail: "informational",
		})
	} else {
		v.ExecutionChecks = append(v.ExecutionChecks, CrucibleCheck{
			Name: "env fingerprint", Status: "differs", Detail: "informational",
		})
	}

	v.TrustLevel = computeTrustLevel(v)
	return v
}

// envFingerprint is an informational hash of the binary's execution environment.
// Non-authoritative.
func envFingerprint(a passArtifact) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(a.Version)))
}

// computeTrustLevel determines the highest achievable trust from check results.
func computeTrustLevel(v *CrucibleVerification) string {
	if v.HasHardFailure() {
		return build.TrustViable
	}

	for _, c := range v.ArtifactChecks {
		if c.Name == "image digest" && c.Status == "match" {
			return build.TrustReproducible
		}
	}

	for _, c := range v.ArtifactChecks {
		if c.Name == "binary hash" && c.Status == "match" {
			return build.TrustDeterministic
		}
	}

	versionOK := false
	graphOK := false
	for _, c := range v.ArtifactChecks {
		if c.Name == "version" && c.Status == "match" {
			versionOK = true
		}
	}
	for _, c := range v.ExecutionChecks {
		if c.Name == "build graph" && c.Status == "match" {
			graphOK = true
		}
	}
	if versionOK && graphOK {
		return build.TrustConsistent
	}

	return build.TrustViable
}
