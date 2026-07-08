package commit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// A brand-new branch with NO upstream — the most common "get my branch to the forge" case —
// must actually push and set tracking. Regression lock for the first-push bug: a bare
// `git push origin` with no upstream is rejected by system git, so CreateTracking names the
// destination ref. The scratch clones elsewhere always HAVE an upstream, so this is the only
// test that exercises the no-upstream execute path.
func TestEngineExecute_FirstPushNoUpstream(t *testing.T) {
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	local := filepath.Join(tmp, "local")
	url := "file://" + remote
	sig := &object.Signature{Name: "t", Email: "t@t"}

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	lr, err := git.PlainInit(local, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lr.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	wt, _ := lr.Worktree()
	if err := os.WriteFile(filepath.Join(local, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("a"); err != nil {
		t.Fatal(err)
	}
	head, err := wt.Commit("first", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}

	session, err := gitstate.OpenSyncSession(local)
	if err != nil {
		t.Fatal(err)
	}
	if session.State().UpstreamConfigured {
		t.Fatal("precondition: the branch should have no upstream")
	}
	eng := NewEngine(session, EngineOptions{})
	plan := eng.Plan(gitplan.DefaultPolicy())
	if _, err := eng.Execute(plan, ExecuteOptions{Approved: true}); err != nil {
		t.Fatalf("first-push execute: %v", err)
	}

	// The remote branch is created at HEAD.
	rr, _ := git.PlainOpen(remote)
	rref, err := rr.Reference(plumbing.NewBranchReferenceName("master"), true)
	if err != nil {
		t.Fatalf("first push did not create the remote branch: %v", err)
	}
	if rref.Hash() != head {
		t.Fatal("first push did not land HEAD on the remote")
	}

	// Upstream tracking is configured locally.
	s2, err := gitstate.OpenSyncSession(local)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.State().UpstreamConfigured {
		t.Fatal("first push did not configure upstream tracking")
	}
}
