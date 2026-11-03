package modules

import (
	"bytes"
	"context"
	"go/format"
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("gofmt", func() lint.Module { return &gofmtModule{} })
}

// gofmtModule reports Go files that are not gofmt-formatted, formatting in-process with
// go/format; the formatted file is a proven-safe --fix-safe remediation.
type gofmtModule struct{}

func (m *gofmtModule) Name() string         { return "gofmt" }
func (m *gofmtModule) DefaultEnabled() bool { return true }
func (m *gofmtModule) AutoDetect() []string { return []string{"*.go"} }

func (m *gofmtModule) Check(ctx context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	if filepath.Ext(file.Path) != ".go" || file.Provenance.RelaxHygiene() {
		return nil, nil
	}
	src, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source(src)
	if err != nil {
		// A file that does not parse is the compiler's to report, with its position.
		return nil, nil
	}
	if bytes.Equal(src, formatted) {
		return nil, nil
	}
	return []lint.Finding{{
		File:     file.Path,
		Line:     firstDifferingLine(src, formatted),
		Module:   m.Name(),
		Severity: lint.SeverityWarning,
		Message:  "not gofmt-formatted (stagefreight lint --fix-safe formats it)",
		RuleID:   "gofmt",
		Fix: &lint.Remediation{
			Kind:        "gofmt",
			Start:       0,
			End:         len(src),
			Expected:    string(src),
			Replacement: string(formatted),
		},
	}}, nil
}

// firstDifferingLine is the 1-based line where a and b first differ.
func firstDifferingLine(a, b []byte) int {
	line := 1
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return line
		}
		if a[i] == '\n' {
			line++
		}
	}
	return line
}
