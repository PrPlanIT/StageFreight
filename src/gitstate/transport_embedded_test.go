package gitstate

import (
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// The embedded (go-git) transport is used when no git binary is available or a credential
// was injected. The rest of the suite runs with git present (system transport), so exercise
// the go-git push path directly here to cover BOTH transports.
func TestEmbeddedTransport_Push(t *testing.T) {
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	seed := filepath.Join(tmp, "seed")
	local := filepath.Join(tmp, "local")
	url := "file://" + remote
	sig := &object.Signature{Name: "t", Email: "t@t"}

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	sr, err := git.PlainInit(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sr.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	swt, _ := sr.Worktree()
	if err := os.WriteFile(filepath.Join(seed, "base"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := swt.Add("base"); err != nil {
		t.Fatal(err)
	}
	if _, err := swt.Commit("base", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	if err := sr.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := git.PlainClone(local, false, &git.CloneOptions{URL: url}); err != nil {
		t.Fatal(err)
	}

	lr, _ := git.PlainOpen(local)
	lwt, _ := lr.Worktree()
	if err := os.WriteFile(filepath.Join(local, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lwt.Add("a"); err != nil {
		t.Fatal(err)
	}
	lhash, err := lwt.Commit("local a", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}

	// The embedded transport push path (no auth needed for a file:// remote).
	tr := &embeddedTransport{repo: lr, auth: nil}
	if err := tr.Push("origin", ""); err != nil {
		t.Fatalf("embedded transport push: %v", err)
	}
	rr, _ := git.PlainOpen(remote)
	rref, err := rr.Reference(plumbing.NewBranchReferenceName("master"), true)
	if err != nil {
		t.Fatalf("remote master ref: %v", err)
	}
	if rref.Hash() != lhash {
		t.Fatal("embedded transport push did not advance the remote")
	}
}
