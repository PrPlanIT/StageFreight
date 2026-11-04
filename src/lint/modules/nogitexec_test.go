package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func TestNoGitExec(t *testing.T) {
	src := `package x

func a() { exec.Command("git", "status") }
func b(ctx context.Context) { exec.CommandContext(ctx, "git", "fetch") }
func c(ctx context.Context) { exec.CommandContext(context.Background(), "git") }
func d() { exec.Command("gitleaks") }
func e(ctx context.Context) { exec.CommandContext(ctx, "go", "git") }
`
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := (&noGitExecModule{}).Check(context.Background(), lint.FileInfo{Path: "x.go", AbsPath: path})
	if err != nil {
		t.Fatal(err)
	}
	got := map[int]bool{}
	for _, x := range f {
		got[x.Line] = true
	}
	for _, line := range []int{3, 4, 5} {
		if !got[line] {
			t.Errorf("line %d runs git and isn't flagged: %v", line, got)
		}
	}
	for _, line := range []int{6, 7} {
		if got[line] {
			t.Errorf("line %d doesn't run git but is flagged", line)
		}
	}
}
