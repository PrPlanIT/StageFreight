package modules

import (
	"bytes"
	"context"
	"os"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("lineendings", func() lint.Module { return &lineEndingsModule{} })
}

type lineEndingsModule struct{}

func (m *lineEndingsModule) Name() string        { return "lineendings" }
func (m *lineEndingsModule) DefaultEnabled() bool { return true }
func (m *lineEndingsModule) AutoDetect() []string { return nil }

func (m *lineEndingsModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
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
		})
	}

	// Trailing whitespace — scan line by line
	lines := bytes.Split(data, []byte("\n"))
	for i, line := range lines {
		// Skip last empty element from trailing newline split
		if i == len(lines)-1 && len(line) == 0 {
			continue
		}
		trimmed := bytes.TrimRight(line, " \t\r")
		if len(trimmed) < len(line) {
			stripped := bytes.TrimRight(line, "\r")
			if len(trimmed) < len(stripped) {
				findings = append(findings, lint.Finding{
					File:     file.Path,
					Line:     i + 1,
					Module:   m.Name(),
					Severity: lint.SeverityInfo,
					Message:  "trailing whitespace",
				})
			}
		}
	}

	// Missing final newline
	if len(data) > 0 && data[len(data)-1] != '\n' {
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     len(lines),
			Module:   m.Name(),
			Severity: lint.SeverityInfo,
			Message:  "missing final newline",
		})
	}

	return findings, nil
}
