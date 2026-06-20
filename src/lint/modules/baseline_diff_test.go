package modules

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// In package modules so the registered lint modules (init()) are available to the base
// re-lint pass. Fixture: a two-commit repo so HEAD has a parent (the baseline). x.go has
// a finding at base AND is unchanged → not new. y.go gains a finding in the working tree
// → new. Proves the diff isolates genuinely-introduced findings from pre-existing ones.
func TestBaselineNewFindings(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Now()}}); err != nil {
			t.Fatal(err)
		}
	}

	write("x.go", "package x   \n") // trailing ws present at base
	write("y.go", "package y\n")    // clean at base
	commit("c1")
	write("z.go", "package z\n") // second commit so HEAD has a parent (= baseline)
	commit("c2")

	os.WriteFile(filepath.Join(dir, "y.go"), []byte("package y   \n"), 0o644) // y.go gains trailing ws

	base, ok, err := lint.ResolveBaseline(dir, "master")
	if err != nil || !ok {
		t.Fatalf("ResolveBaseline ok=%v err=%v", ok, err)
	}

	fX := lint.Finding{File: "x.go", Module: "lineendings", RuleID: "trailing-whitespace", Anchor: "package x"}
	fY := lint.Finding{File: "y.go", Module: "lineendings", RuleID: "trailing-whitespace", Anchor: "package y"}

	newFp, err := base.NewFindings([]lint.Finding{fX, fY}, config.DefaultLintConfig(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if newFp[fX.Fingerprint()] {
		t.Error("x.go finding is unchanged since base — must NOT be new")
	}
	if !newFp[fY.Fingerprint()] {
		t.Error("y.go finding was introduced in the working tree — must be new")
	}
}
