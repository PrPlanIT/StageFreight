package lint

import "fmt"

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

// Finding represents a single lint result.
type Finding struct {
	File     string
	Line     int
	Column   int
	Module   string
	Severity Severity
	Message  string
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
type Remediation struct {
	Kind        string // "trailing-whitespace" | "final-newline"
	Start, End  int
	Replacement string
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
