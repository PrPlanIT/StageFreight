package modules

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("conflicts", func() lint.Module { return &conflictsModule{} })
}

type conflictsModule struct{}

func (m *conflictsModule) Name() string         { return "conflicts" }
func (m *conflictsModule) DefaultEnabled() bool { return true }
func (m *conflictsModule) AutoDetect() []string { return nil }

func (m *conflictsModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	var findings []lint.Finding

	// Check for merge conflict markers
	markerFindings, err := m.checkMarkers(file)
	if err != nil {
		return nil, err
	}
	findings = append(findings, markerFindings...)

	return findings, nil
}

func (m *conflictsModule) checkMarkers(file lint.FileInfo) ([]lint.Finding, error) {
	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []lint.Finding
	scanner := bufio.NewScanner(f)
	lineNum := 0
	inConflict := false // inside an unterminated <<<<<<< ... >>>>>>> region

	flag := func(marker string) {
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     lineNum,
			Module:   m.Name(),
			Severity: lint.SeverityCritical,
			Message:  "merge conflict marker: " + marker,
		})
	}

	for scanner.Scan() {
		lineNum++
		trimmed := strings.TrimSpace(scanner.Text())

		switch {
		case isConflictMarker(trimmed, '<'):
			// Opening marker — never appears decoratively.
			flag("<<<<<<<")
			inConflict = true
		case isConflictMarker(trimmed, '>'):
			// Closing marker — never appears decoratively.
			flag(">>>>>>>")
			inConflict = false
		case isConflictMarker(trimmed, '='):
			// A "=======" separator is a conflict marker ONLY between an open
			// <<<<<<< and its >>>>>>>. A lone ======= is a markdown setext-header
			// underline or a horizontal rule, not a conflict — don't flag it.
			if inConflict {
				flag("=======")
			}
		}
	}

	return findings, scanner.Err()
}

// isConflictMarker reports whether trimmed is a git conflict marker built from ch:
// EXACTLY seven of ch, then end-of-line or a single space (an optional label such as
// "HEAD" or a branch name may follow). Eight-or-more runs (decorative rules,
// "========================") are rejected, so markdown setext underlines and ASCII
// rules never register as markers.
func isConflictMarker(trimmed string, ch byte) bool {
	if len(trimmed) < 7 {
		return false
	}
	for i := 0; i < 7; i++ {
		if trimmed[i] != ch {
			return false
		}
	}
	if len(trimmed) == 7 {
		return true // exactly "<<<<<<<" / "=======" / ">>>>>>>"
	}
	if trimmed[7] == ch {
		return false // 8+ of the char → decorative rule, not a marker
	}
	return trimmed[7] == ' ' // "<<<<<<< HEAD", ">>>>>>> branch"
}

// CheckFilenameCollisions detects case-insensitive filename collisions across a set of files.
// Called separately from the per-file Check because it needs the full file list.
func CheckFilenameCollisions(files []lint.FileInfo) []lint.Finding {
	seen := make(map[string]string) // lowercase path -> original path
	var findings []lint.Finding

	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(f.Path))
		if original, exists := seen[lower]; exists && original != f.Path {
			findings = append(findings, lint.Finding{
				File:     f.Path,
				Line:     1,
				Module:   "conflicts",
				Severity: lint.SeverityWarning,
				Message:  "case-insensitive filename collision with " + original,
			})
		} else {
			seen[lower] = f.Path
		}
	}

	return findings
}
