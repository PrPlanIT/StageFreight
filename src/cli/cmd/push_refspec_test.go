package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// divergedRemote builds a bare remote whose main moved past the commit a local clone
// sits on, and a local commit on top of that older base. It returns the clone's dir and
// the remote's path.
func divergedRemote(t *testing.T) (local, remote string) {
	t.Helper()
	tmp := t.TempDir()
	remote = filepath.Join(tmp, "remote.git")
	bare, err := git.PlainInit(remote, true)
	if err != nil {
		t.Fatal(err)
	}
	mainRef := plumbing.NewBranchReferenceName("main")
	if err := bare.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, mainRef)); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@t", When: time.Now()}
	commit := func(dir, file, msg string) {
		t.Helper()
		repo, err := git.PlainOpen(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		wt, _ := repo.Worktree()
		if _, err := wt.Add(file); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: sig}); err != nil {
			t.Fatal(err)
		}
	}
	clone := func(name string) string {
		t.Helper()
		dir := filepath.Join(tmp, name)
		if _, err := git.PlainClone(dir, false, &git.CloneOptions{URL: remote}); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	seed := filepath.Join(tmp, "seed")
	repo, err := git.PlainInit(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{remote}}); err != nil {
		t.Fatal(err)
	}
	commit(seed, "base", "base")
	if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"refs/heads/master:refs/heads/main"}}); err != nil {
		t.Fatal(err)
	}
	local = clone("local")
	other := clone("other")
	commit(other, "moved", "remote moved")
	otherRepo, _ := git.PlainOpen(other)
	if err := otherRepo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"refs/heads/main:refs/heads/main"}}); err != nil {
		t.Fatal(err)
	}
	commit(local, "work", "local work")
	return local, remote
}

func remoteMainLog(t *testing.T, remote string) []string {
	t.Helper()
	repo, err := git.PlainOpen(remote)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := repo.Reference(plumbing.NewBranchReferenceName("main"), true)
	if err != nil {
		t.Fatal(err)
	}
	iter, err := repo.Log(&git.LogOptions{From: ref.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	_ = iter.ForEach(func(c *object.Commit) error {
		msgs = append(msgs, c.Message)
		return nil
	})
	return msgs
}

// A refspec push onto a branch that moved replays the local commit onto its new tip —
// the default — and fails with --no-rebase. No git binary is involved.
func TestRefspecPush_ReplaysOntoMovedBranch(t *testing.T) {
	t.Setenv("PATH", "")

	local, remote := divergedRemote(t)
	if err := refspecPush(local, "origin", "HEAD:refs/heads/main", false, false); err == nil {
		t.Fatal("with replay off, a push onto a moved branch must be refused")
	}
	if err := refspecPush(local, "origin", "HEAD:refs/heads/main", true, false); err != nil {
		t.Fatalf("refspec push onto a moved branch: %v", err)
	}
	log := remoteMainLog(t, remote)
	if len(log) != 3 || log[0] != "local work" || log[1] != "remote moved" {
		t.Fatalf("remote main = %q, want local work replayed on top of remote moved", log)
	}
}
