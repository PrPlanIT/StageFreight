package modules

import (
	"context"
	"os"
	"regexp"
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
		// A generic-api-key hit whose extracted value IS a code numeric literal (a CPUID vendor
		// tag, a magic number) is objectively not a credential, so it is dropped entirely.
		if h.RuleID == "generic-api-key" && isCodeConstant(h.Secret) {
			continue
		}
		// Severity is gated on confidence. A structurally-identified credential — a specific
		// provider rule or a real private-key block — is Confirmed → Critical and blocks the
		// gate. A generic-api-key entropy match is Probable/Heuristic → Warning: surfaced for
		// review but NOT fatal, because that catch-all rule is the high-false-positive class
		// (test fixtures, doc examples). A genuine key that only matches heuristically is still
		// shown; suppress a reviewed false positive with `lint.exclude` rather than lowering the
		// bar globally — so "someone put a real key in a test" still surfaces for a human.
		conf := secretConfidence(h.RuleID, h.Entropy)
		findings = append(findings, lint.Finding{
			File:       file.Path,
			Line:       h.StartLine + 1, // gitleaks is 0-indexed
			Module:     m.Name(),
			Severity:   severityForConfidence(conf),
			Confidence: conf,
			Message:    h.Description + " (" + h.RuleID + ")",
		})
	}
	return findings, nil
}

// codeConstantRe matches a value that IS a small hex integer literal — optionally prefixed by
// an identifier like a register name ("EBX=0x68747541") — i.e. a Go/Rust/C numeric constant,
// not a credential (CPUID vendor tags, magic numbers).
//
// This is a HARD classifier: it DROPS findings, so a false match would HIDE a real secret. It
// is therefore deliberately strict, matching the lockfile-checksum exclusions in conservatism:
//   - ANCHORED — the extracted value must BE the literal, not merely contain a hex substring,
//     so a real credential that happens to include "0x…" is not classified out.
//   - BOUNDED to ≤16 hex digits — a genuine numeric literal fits in a u64; a copied hash or a
//     hex-encoded key is longer and stays flagged.
//   - scoped to generic-api-key only; specific provider rules fire independently.
var codeConstantRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\s*=\s*)?0x[0-9a-fA-F]{2,16}$`)

// isCodeConstant reports whether a gitleaks-extracted value is structurally a code numeric
// literal (and so cannot be a credential). Conservative by construction — see codeConstantRe.
func isCodeConstant(secret string) bool {
	return codeConstantRe.MatchString(strings.TrimSpace(secret))
}

// genericKeyCredentialEntropy is the entropy above which a generic-api-key match is treated
// as a credential-grade (Probable) hit rather than a weak (Heuristic) one. Both map to a
// Warning (generic-api-key is the false-positive-prone class); the split is review priority.
const genericKeyCredentialEntropy = 4.5

// secretConfidence reports how strongly the evidence supports a credential. A SPECIFIC provider
// rule (aws, github, stripe, …) or a real private-key block structurally identifies a credential
// → Confirmed, which the caller maps to Critical (blocking). The catch-all generic-api-key is an
// entropy heuristic: a credential-grade hit is Probable, a weaker one Heuristic — both map to a
// Warning (surfaced for review, not fatal), since generic matches are the false-positive-prone
// class. A real key that only matches heuristically is still shown; confirm it, or suppress a
// reviewed false positive with lint.exclude.
func secretConfidence(ruleID string, entropy float32) lint.Confidence {
	if ruleID != "generic-api-key" {
		return lint.ConfidenceConfirmed
	}
	if entropy < genericKeyCredentialEntropy {
		return lint.ConfidenceHeuristic
	}
	return lint.ConfidenceProbable
}

// severityForConfidence maps credential confidence to gate severity. A Confirmed credential is
// Critical — it blocks the gate (Finding.Blocks) and is Fatal for mutation safety. A Probable
// or Heuristic hit is a Warning: surfaced for review but non-blocking, so the false-positive-
// prone generic-api-key class can't fail a pipeline on a test fixture. A reviewed false positive
// is suppressed per-project with lint.exclude; a real key that only matches heuristically still
// shows up for a human to catch.
func severityForConfidence(c lint.Confidence) lint.Severity {
	if c == lint.ConfidenceConfirmed {
		return lint.SeverityCritical
	}
	return lint.SeverityWarning
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
