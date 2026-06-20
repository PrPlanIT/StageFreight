package lint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Severity indicates how serious a finding is.
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return fmt.Sprintf("severity(%d)", int(s))
	}
}

// Confidence is how strongly the evidence supports a finding — ORTHOGONAL to Severity
// (which is impact-IF-true). A structurally-identified match is Confirmed; a strong but
// non-structural signal is Probable; a weak heuristic (e.g. an entropy guess that also
// matches hex constants / magic numbers) is Heuristic. Separating the two stops Severity
// from being overloaded to mean both "how bad" and "how sure".
//
// The zero value is Confirmed, so any finding that does not set Confidence gates exactly as
// before — only a module with genuine doubt downgrades confidence.
type Confidence int

const (
	ConfidenceConfirmed Confidence = iota // default: structurally identified / authoritative
	ConfidenceProbable                    // strong evidence, not structural
	ConfidenceHeuristic                   // weak/ambiguous — surfaced, but does not gate CI
)

func (c Confidence) String() string {
	switch c {
	case ConfidenceProbable:
		return "probable"
	case ConfidenceHeuristic:
		return "heuristic"
	default:
		return "confirmed"
	}
}

// Finding represents a single lint result.
type Finding struct {
	File       string
	Line       int
	Column     int
	Module     string
	Severity   Severity
	Confidence Confidence
	Message    string
	// RuleID is a STABLE internal identifier for the finding kind (e.g.
	// "trailing-whitespace"). It is the identity surface for baseline diffing and must
	// not change for cosmetic reasons — unlike Message, which is presentation and may be
	// reworded freely. Empty is allowed; identity then falls back to Module.
	RuleID string
	// Anchor is a normalized SEMANTIC anchor (e.g. the trimmed line content) that ties a
	// finding's identity to what it is about rather than where it sits. It lets a finding
	// survive line-number shifts so a moved issue isn't mistaken for a new one. Empty is
	// allowed (coarser identity).
	Anchor string
	// Fix, when non-nil, is a proven-safe, byte-exact, reversible remediation the
	// detector emits alongside the finding. Because the edit is carried by the finding
	// itself — not re-derived by a separate fixer — "what gets fixed" equals "what was
	// reported," by construction. A nil Fix means the finding is NOT auto-fixable, so no
	// flag can ever mutate it. Disclosures are not Findings and so structurally cannot
	// carry a Fix.
	Fix *Remediation
}

// Remediation is a single byte-exact edit: replace File[Start:End] with Replacement
// ("" = deletion). Kind names the safe-edit category for granular opt-in and reporting.
// The applier is dumb — it performs exactly this span replacement and re-derives
// nothing — which is what keeps remediation tied to the reported finding.
//
// Expected is the precondition: the exact bytes the detector saw at [Start:End]. The
// applier performs a compare-and-swap — it mutates ONLY if the file still holds those
// bytes — so a stale finding against a since-changed file (a race, a replay, an edit
// between detect and fix) is skipped, never misapplied. Mutates only with proof, the
// mirror of "classification only relaxes with proof."
type Remediation struct {
	Kind        string // "trailing-whitespace" | "final-newline"
	Start, End  int
	Expected    string // bytes that must currently occupy [Start:End], or the CAS skips
	Replacement string
}

// Blocks reports whether a finding should fail CI: critical impact AND at least probable
// confidence. A critical-but-Heuristic finding (e.g. a low-entropy generic-key guess) is
// surfaced at full severity but does not block — impact and certainty are separate gates.
func (f Finding) Blocks() bool {
	return f.Severity == SeverityCritical && f.Confidence != ConfidenceHeuristic
}

// Fingerprint is the line-INDEPENDENT identity of a finding, for baseline diffing:
// hash(File + Module + RuleID + Anchor). Deliberately excludes Line/Column (position is
// not identity) and Message (presentation is not identity), so a finding that merely
// moved or was reworded keeps the same fingerprint and is not mistaken for new. Identical
// anchors collide — which biases toward UNDERcounting "new" (safe silence over false
// accusation), the correct bias for a trust-first tool.
func (f Finding) Fingerprint() string {
	h := sha256.New()
	for _, part := range []string{f.File, f.Module, f.RuleID, f.Anchor} {
		h.Write([]byte(part))
		h.Write([]byte{0}) // domain separator so field boundaries can't be forged by concatenation
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FileInfo is passed to each module for inspection. Content is the centrally-computed
// classification (text/binary/ambiguous): text modules route on it, byte modules
// ignore it. Its zero value is ContentText, so an unclassified file behaves as text.
type FileInfo struct {
	Path    string // relative path from repo root
	AbsPath string // absolute path on disk
	Size    int64
	Content Content
	// Provenance is the centrally-computed origin label (authored/generated/vendored/
	// lockfile). Authored-hygiene modules relax on non-authored; security and
	// supply-chain modules ignore it. Zero value is authored (full scrutiny).
	Provenance Provenance
}
