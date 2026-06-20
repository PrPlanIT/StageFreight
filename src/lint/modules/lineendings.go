package modules

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("lineendings", func() lint.Module { return &lineEndingsModule{} })
}

type lineEndingsModule struct{}

func (m *lineEndingsModule) Name() string         { return "lineendings" }
func (m *lineEndingsModule) DefaultEnabled() bool { return true }
func (m *lineEndingsModule) AutoDetect() []string { return nil }

func (m *lineEndingsModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	// Text-oriented module: line-ending semantics are meaningless on binary content.
	if !file.Content.IsText() {
		return nil, nil
	}
	// Authored-code hygiene: stand down on generated/vendored/lockfile content — the
	// vendored rqlite-rs crate's CRLF is the vendor's, not ours to nag about.
	if file.Provenance.RelaxHygiene() {
		return nil, nil
	}
	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return nil, nil
	}

	var findings []lint.Finding

	// Count line ending types
	crlfCount := bytes.Count(data, []byte("\r\n"))
	// LF-only count: total \n minus those that are part of \r\n
	lfCount := bytes.Count(data, []byte("\n")) - crlfCount

	// Mixed line endings
	if crlfCount > 0 && lfCount > 0 {
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     1,
			Module:   m.Name(),
			Severity: lint.SeverityWarning,
			Message:  "mixed line endings (CRLF and LF)",
			RuleID:   "mixed-line-endings",
		})
	}

	// Pure CRLF files
	if crlfCount > 0 && lfCount == 0 {
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     1,
			Module:   m.Name(),
			Severity: lint.SeverityInfo,
			Message:  "file uses CRLF line endings",
			RuleID:   "crlf",
		})
	}

	// Markdown two-or-more trailing spaces are a hard line break (CommonMark) — so
	// trailing whitespace there is NOT safely auto-fixable; we still report it, but emit
	// no Fix. Stripping it would silently delete an intended <br>.
	mdHardBreak := isMarkdown(file.Path)

	// Trailing whitespace — scan line by line, tracking byte offsets so a safe Fix can
	// carry the exact span (the space/tab between content and any CR).
	lines := bytes.Split(data, []byte("\n"))
	offset := 0
	for i, line := range lines {
		lineStart := offset
		offset += len(line) + 1 // advance past this line and the \n that split it
		// Skip last empty element from trailing newline split
		if i == len(lines)-1 && len(line) == 0 {
			continue
		}
		trimmed := bytes.TrimRight(line, " \t\r")
		if len(trimmed) < len(line) {
			stripped := bytes.TrimRight(line, "\r")
			if len(trimmed) < len(stripped) {
				f := lint.Finding{
					File:     file.Path,
					Line:     i + 1,
					Module:   m.Name(),
					Severity: lint.SeverityInfo,
					Message:  "trailing whitespace",
					RuleID:   "trailing-whitespace",
					Anchor:   string(trimmed), // the line's content sans trailing ws — survives line moves
				}
				// Safe Fix: delete the trailing space/tab, KEEP the CR. The span runs from
				// the end of trimmed content to the start of the CR (or EOL); Expected is
				// exactly those whitespace bytes, so the applier's CAS confirms them first.
				if !mdHardBreak {
					f.Fix = &lint.Remediation{
						Kind:     "trailing-whitespace",
						Start:    lineStart + len(trimmed),
						End:      lineStart + len(stripped),
						Expected: string(line[len(trimmed):len(stripped)]),
					}
				}
				findings = append(findings, f)
			}
		}
	}

	// Missing final newline — appending one is POSIX-correct and semantics-neutral, so
	// the Fix is always safe (Markdown included: a final newline is not a hard break).
	// Modeled as a CAS-guarded replacement of the last byte with itself + "\n" (rather
	// than a bare end-insertion) so a since-grown file can't get a newline spliced into
	// its middle: if the last byte changed, the compare-and-swap skips it.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		last := data[len(data)-1]
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     len(lines),
			Module:   m.Name(),
			Severity: lint.SeverityInfo,
			Message:  "missing final newline",
			RuleID:   "missing-final-newline",
			Fix: &lint.Remediation{
				Kind:        "final-newline",
				Start:       len(data) - 1,
				End:         len(data),
				Expected:    string(last),
				Replacement: string(last) + "\n",
			},
		})
	}

	return findings, nil
}

// isMarkdown reports whether the path is a Markdown document, where trailing spaces can
// be semantically meaningful (hard line breaks).
func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}
