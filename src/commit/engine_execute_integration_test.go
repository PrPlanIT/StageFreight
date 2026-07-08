package commit

import (
	"errors"
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

// execScratch builds a bare remote seeded with a base commit, plus local + other clones
// (upstream tracking configured), and returns a commitFile helper.
func execScratch(t *testing.T) (remote, local, other string, commitFile func(dir, name, content, msg string)) {
	t.Helper()
	tmp := t.TempDir()
	remote = filepath.Join(tmp, "remote.git")
	seed := filepath.Join(tmp, "seed")
	local = filepath.Join(tmp, "local")
	other = filepath.Join(tmp, "other")
	url := "file://" + remote
	sig := &object.Signature{Name: "t", Email: "t@t"}

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	commitFile = func(dir, name, content, msg string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := git.PlainOpen(dir)
		if err != nil {
			t.Fatal(err)
		}
		wt, _ := r.Worktree()
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: sig}); err != nil {
			t.Fatal(err)
		}
	}

	seedRepo, err := git.PlainInit(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedRepo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	commitFile(seed, "base", "base\n", "base")
	if err := seedRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}
	if _, err := git.PlainClone(local, false, &git.CloneOptions{URL: url}); err != nil {
		t.Fatal(err)
	}
	if _, err := git.PlainClone(other, false, &git.CloneOptions{URL: url}); err != nil {
		t.Fatal(err)
	}
	return remote, local, other, commitFile
}

func remoteMaster(t *testing.T, remote string) plumbing.Hash {
	t.Helper()
	r, err := git.PlainOpen(remote)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Reference(plumbing.NewBranchReferenceName("master"), true)
	if err != nil {
		t.Fatalf("remote master ref: %v", err)
	}
	return ref.Hash()
}

func headHash(t *testing.T, dir string) plumbing.Hash {
	t.Helper()
	r, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := r.Head()
	if err != nil {
		t.Fatal(err)
	}
	return ref.Hash()
}

// Upload actually pushes: the remote advances to local HEAD.
func TestEngineExecute_Upload(t *testing.T) {
	remote, local, _, commitFile := execScratch(t)
	commitFile(local, "a", "a\n", "local a")

	sess, err := gitstate.OpenSyncSession(local)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(sess, EngineOptions{})
	plan := eng.Plan(gitplan.Policy{})
	if !kindsEq(planKinds(plan), gitplan.OpUpload) {
		t.Fatalf("want [upload], got %v", planKinds(plan))
	}
	res, err := eng.Execute(plan, ExecuteOptions{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !kindsEq(res.Performed, gitplan.OpUpload) {
		t.Fatalf("performed %v", res.Performed)
	}
	if remoteMaster(t, remote) != headHash(t, local) {
		t.Fatal("remote master did not advance to local HEAD after upload")
	}
}

// Diverged: Confirm gates (no mutation without approval), Decide refuses, approved
// Replay+Upload actually lands linearly.
func TestEngineExecute_DivergedGates(t *testing.T) {
	remote, local, other, commitFile := execScratch(t)
	commitFile(local, "a", "a\n", "local a") // local ahead by a
	commitFile(other, "b", "b\n", "other b")
	otherR, _ := git.PlainOpen(other)
	if err := otherR.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("divergent push: %v", err)
	}

	sess, err := gitstate.OpenSyncSession(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	eng := NewEngine(sess, EngineOptions{})
	branch := sess.State().Branch
	before := remoteMaster(t, remote) // base+b

	// Confirm gate — protected diverged, NOT approved → refuse + no mutation.
	planProt := eng.Plan(gitplan.Policy{Protected: []string{branch}})
	_, err = eng.Execute(planProt, ExecuteOptions{Approved: false})
	var cr *ConfirmRequiredError
	if !errors.As(err, &cr) {
		t.Fatalf("want ConfirmRequiredError, got %T: %v", err, err)
	}
	if remoteMaster(t, remote) != before {
		t.Fatal("confirm gate must not mutate the remote")
	}

	// Decide — feature diverged, no policy → refuse + no mutation.
	planFeat := eng.Plan(gitplan.Policy{})
	_, err = eng.Execute(planFeat, ExecuteOptions{})
	var dr *DecisionRequiredError
	if !errors.As(err, &dr) {
		t.Fatalf("want DecisionRequiredError, got %T: %v", err, err)
	}
	if remoteMaster(t, remote) != before {
		t.Fatal("decide must not mutate the remote")
	}

	// Approved — protected diverged → Replay + Upload runs; remote advances linearly.
	res, err := eng.Execute(planProt, ExecuteOptions{Approved: true})
	if err != nil {
		t.Fatalf("approved execute: %v", err)
	}
	if !kindsEq(res.Performed, gitplan.OpConfirm, gitplan.OpReplay, gitplan.OpUpload) {
		t.Fatalf("performed %v", res.Performed)
	}
	if remoteMaster(t, remote) == before {
		t.Fatal("approved replay+upload must advance the remote")
	}
	verify := filepath.Join(t.TempDir(), "verify")
	if _, err := git.PlainClone(verify, false, &git.CloneOptions{URL: "file://" + remote}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"base", "a", "b"} {
		if _, err := os.Stat(filepath.Join(verify, f)); err != nil {
			t.Fatalf("remote missing %s after replay: %v", f, err)
		}
	}
}
