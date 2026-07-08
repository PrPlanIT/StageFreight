package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func pushScratch(t *testing.T) (remote, local, other string, commitFile func(dir, name, content, msg string)) {
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

func remoteMasterHash(t *testing.T, remote string) plumbing.Hash {
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

// `sf push` on a clean-ahead branch uploads without friction, even on a protected branch.
func TestPlannerPush_AheadUploads(t *testing.T) {
	remote, local, _, commitFile := pushScratch(t)
	commitFile(local, "a", "a\n", "local a")

	var buf bytes.Buffer
	if err := runPlannerPush(local, "origin", false, &buf); err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.Contains(buf.String(), "upload") {
		t.Fatalf("expected an upload plan; output:\n%s", buf.String())
	}
	local2, _ := git.PlainOpen(local)
	head, _ := local2.Head()
	if remoteMasterHash(t, remote) != head.Hash() {
		t.Fatal("remote did not advance after clean-ahead push")
	}
}

// `sf push` on a diverged PROTECTED branch (master) refuses without --yes (no mutation),
// and proceeds with it — the headline behavior change: never a silent rewrite.
func TestPlannerPush_ProtectedDivergedGate(t *testing.T) {
	remote, local, other, commitFile := pushScratch(t)
	commitFile(local, "a", "a\n", "local a")
	commitFile(other, "b", "b\n", "other b")
	otherR, _ := git.PlainOpen(other)
	if err := otherR.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("divergent push: %v", err)
	}
	before := remoteMasterHash(t, remote)

	// No --yes → refuse + no mutation.
	var buf bytes.Buffer
	if err := runPlannerPush(local, "origin", false, &buf); err == nil {
		t.Fatal("expected a non-nil error (confirmation required)")
	}
	if !strings.Contains(buf.String(), "needs confirmation") {
		t.Fatalf("expected a confirmation prompt; output:\n%s", buf.String())
	}
	if remoteMasterHash(t, remote) != before {
		t.Fatal("protected diverged push must NOT mutate the remote without --yes")
	}

	// With --yes → replay + upload proceeds.
	var buf2 bytes.Buffer
	if err := runPlannerPush(local, "origin", true, &buf2); err != nil {
		t.Fatalf("approved push: %v", err)
	}
	if remoteMasterHash(t, remote) == before {
		t.Fatal("approved push must advance the remote")
	}
}
