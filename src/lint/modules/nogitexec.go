package modules

import (
	"bufio"
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

// noGitExecModule forbids exec.Command("git") outside its sanctioned sites.
// The runtime image has no git binary by design; git operations must use go-git
// there. Exceptions: src/sync/git_mirror.go (mirror push CLI dependency, tracked:
// forge-sync) and src/gitstate/transport_system.go (the systemTransport, which
// execs git ONLY when gitAvailable() is true — the runtime, having no git, always
// falls back to the embedded go-git transport, so it never execs git there).
type noGitExecModule struct{}

func (m *noGitExecModule) Name() string         { return "no-git-exec" }
func (m *noGitExecModule) DefaultEnabled() bool { return true }
func (m *noGitExecModule) AutoDetect() []string { return []string{"**/*.go"} }

var gitExecRe = regexp.MustCompile(`exec\.Command(Context)?\(\s*"git"`)

func (m *noGitExecModule) Check(_ context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	p := filepath.ToSlash(file.Path)
	// Permitted sites:
	//   git_mirror.go       — mirror push retains CLI dependency (tracked: forge-sync)
	//   transport_system.go — systemTransport; execs git ONLY when gitAvailable(),
	//                         the runtime (no git) uses embedded go-git instead
	//   nogitexec.go        — this module; pattern appears in comments and the error message
	//   *_test.go           — tests run in the builder image which has git, not the runtime image
	if p == "src/sync/git_mirror.go" ||
		p == "src/gitstate/transport_system.go" ||
		p == "src/lint/modules/nogitexec.go" ||
		strings.HasSuffix(p, "_test.go") {
		return nil, nil
	}

	f, err := os.Open(file.AbsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var findings []lint.Finding
	scanner := bufio.NewScanner(f)
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
