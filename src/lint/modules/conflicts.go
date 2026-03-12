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

func (m *conflictsModule) Name() string        { return "conflicts" }
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

	markers := []string{
		"<<<<<<<",
		"=======",
		">>>>>>>",
	}

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		for _, marker := range markers {
			if strings.HasPrefix(trimmed, marker) {
				findings = append(findings, lint.Finding{
					File:     file.Path,
					Line:     lineNum,
					Module:   m.Name(),
					Severity: lint.SeverityCritical,
					Message:  "merge conflict marker: " + marker,
				})
				break
			}
		}
	}

	return findings, scanner.Err()
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
