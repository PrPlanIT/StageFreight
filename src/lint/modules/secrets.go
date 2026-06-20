package modules

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/zricethezav/gitleaks/v8/detect"
)

func init() {
	lint.Register("secrets", func() lint.Module { return &secretsModule{} })
}

type secretsModule struct {
	once     sync.Once
	detector *detect.Detector
	initErr  error
}

func (m *secretsModule) Name() string         { return "secrets" }
func (m *secretsModule) DefaultEnabled() bool { return true }
func (m *secretsModule) AutoDetect() []string { return nil }

func (m *secretsModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	m.once.Do(func() {
		m.detector, m.initErr = detect.NewDetectorDefaultConfig()
	})
	if m.initErr != nil {
		return nil, m.initErr
	}

	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	hits := m.detector.DetectBytes(data)
	if len(hits) == 0 {
		return nil, nil
	}

	// Provenance informs interpretation, not blanket skipping: lockfiles are still scanned
	// for genuine credentials, but a committed lockfile's checksum/integrity hashes are
	// expected machine artifacts, not secrets — so high-entropy generic-key hits ON those
	// structural lines are suppressed. `resolved` URLs are deliberately NOT suppressed
	// (they can carry embedded credentials).
	lockfile := file.Provenance.Kind == lint.ProvenanceLockfile

	findings := make([]lint.Finding, 0, len(hits))
	for _, h := range hits {
		if lockfile && isLockfileIntegrityLine(h.Line) {
			continue
		}
		findings = append(findings, lint.Finding{
			File:       file.Path,
			Line:       h.StartLine + 1, // gitleaks is 0-indexed
			Module:     m.Name(),
			Severity:   lint.SeverityCritical, // a leaked credential is critical impact IF real
			Confidence: secretConfidence(h.RuleID, h.Entropy),
			Message:    h.Description + " (" + h.RuleID + ")",
		})
	}
	return findings, nil
}

// genericKeyCriticalEntropy is the entropy a generic-api-key match must clear to gate CI
// as critical. gitleaks' default floor (~3.5) is permissive enough to catch hex constants
// and magic numbers (a CPUID 0x68747541 vendor tag reads at ~3.52); real random
// credentials run 4.5+. Below this, the match is weak evidence → surfaced, not blocking.
const genericKeyCriticalEntropy = 4.5

// secretConfidence reports how strongly the evidence supports a credential — separate from
// severity (always critical for a secret, because the IMPACT if real is critical). A
// SPECIFIC provider rule (aws, github, stripe, …) structurally identifies a credential →
// Confirmed. The catch-all generic-api-key is an entropy HEURISTIC: a credential-grade hit
// is Probable; a weak one (hex constants, magic numbers — a CPUID 0x68747541 tag reads at
// ~3.52) is Heuristic, which surfaces it without gating CI on a guess.
func secretConfidence(ruleID string, entropy float32) lint.Confidence {
	if ruleID != "generic-api-key" {
		return lint.ConfidenceConfirmed
	}
	if entropy < genericKeyCriticalEntropy {
		return lint.ConfidenceHeuristic
	}
	return lint.ConfidenceProbable
}

// isLockfileIntegrityLine reports whether a line is a lockfile's structural integrity /
// checksum material — deterministic hashes a package manager writes, never credentials.
// Precise on purpose (no `resolved` URLs, which can embed real secrets).
func isLockfileIntegrityLine(line string) bool {
	l := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(l, "checksum =") || strings.HasPrefix(l, "checksum="): // Cargo.lock
		return true
	case strings.Contains(l, `"integrity":`): // package-lock.json / pnpm
		return true
	case strings.HasPrefix(l, "integrity ") && strings.Contains(l, "sha"): // yarn.lock
		return true
	case strings.Contains(l, " h1:"): // go.sum module hashes
		return true
	}
	return false
}
