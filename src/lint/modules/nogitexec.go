package modules

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
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

func (m *noGitExecModule) Name() string { return "no-git-exec" }

// DefaultEnabled is false: forbidding a git binary is StageFreight's own architecture, not
// a rule for the projects it lints. StageFreight enables it in its own config.
func (m *noGitExecModule) DefaultEnabled() bool { return false }
func (m *noGitExecModule) AutoDetect() []string { return []string{"**/*.go"} }

func (m *noGitExecModule) Check(_ context.Context, file lint.FileInfo) ([]lint.Finding, error) {
	if strings.HasSuffix(filepath.ToSlash(file.Path), "_test.go") {
		return nil, nil // tests may run git
	}
	src, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file.Path, src, 0)
	if err != nil {
		return nil, nil // a file that doesn't parse is gofmt's and the compiler's to report
	}

	var findings []lint.Finding
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !runsGit(call) {
			return true
		}
		findings = append(findings, lint.Finding{
			File:     file.Path,
			Line:     fset.Position(call.Pos()).Line,
			Module:   m.Name(),
			Severity: lint.SeverityCritical,
			Message:  "this runs a git binary, which the runtime doesn't have; use go-git instead",
		})
		return true
	})
	return findings, nil
}

// runsGit reports whether call is exec.Command("git", …) or exec.CommandContext(ctx, "git", …).
func runsGit(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
		return false
	}
	arg := -1
	switch sel.Sel.Name {
	case "Command":
		arg = 0
	case "CommandContext":
		arg = 1
	}
	if arg < 0 || len(call.Args) <= arg {
		return false
	}
	lit, ok := call.Args[arg].(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && strconv.Quote("git") == lit.Value
}
