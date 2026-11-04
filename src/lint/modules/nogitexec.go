package modules

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func init() {
	lint.Register("no-git-exec", func() lint.Module {
		return &noGitExecModule{}
	})
}

// noGitExecModule forbids exec.Command("git"): StageFreight's git operations use go-git
// (src/gitstate); only tests may run a git binary, which the substrate provides them.
type noGitExecModule struct{}

func (m *noGitExecModule) Name() string         { return "no-git-exec" }
func (m *noGitExecModule) DefaultEnabled() bool { return true }
func (m *noGitExecModule) AutoDetect() []string { return []string{"**/*.go"} }

var gitExecRe = regexp.MustCompile(`exec\.Command(Context)?\(\s*"git"`)

func (m *noGitExecModule) Check(_ context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	p := filepath.ToSlash(file.Path)
	// Permitted: this module (the pattern appears in its own text) and tests.
	if p == "src/lint/modules/nogitexec.go" || strings.HasSuffix(p, "_test.go") {
		return nil, nil
	}

	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []lint.Finding
	scanner := lint.NewLineScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if gitExecRe.MatchString(scanner.Text()) {
			findings = append(findings, lint.Finding{
				File:     file.Path,
				Line:     lineNum,
				Module:   m.Name(),
				Severity: lint.SeverityCritical,
				Message:  `the runtime image has no git binary; use go-git via github.com/PrPlanIT/StageFreight/src/gitstate instead of exec.Command("git")`,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return findings, nil
}
