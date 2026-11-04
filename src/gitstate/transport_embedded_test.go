package gitstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// A local remote is cloned from and pushed to in-process, with no git binary anywhere.
func TestEmbeddedTransport_Push(t *testing.T) {
	t.Setenv("PATH", "")
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

	tr := newEmbeddedTransport(lr, nil)
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

// Refspecs resolve as git resolves them: go-git takes only full ref names, and a short
// destination sent as-is is refused by the server ("funny refname").
func TestEmbeddedTransport_PushRefSpec(t *testing.T) {
	t.Setenv("PATH", "")
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	local := filepath.Join(tmp, "local")
	sig := &object.Signature{Name: "t", Email: "t@t"}

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	lr, err := git.PlainInit(local, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lr.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{"file://" + remote}}); err != nil {
		t.Fatal(err)
	}
	wt, _ := lr.Worktree()
	if err := os.WriteFile(filepath.Join(local, "a"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("a"); err != nil {
		t.Fatal(err)
	}
	head, err := wt.Commit("a", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}
	// Local branch "work" (checked out), branch "main", tag "v1", and a name that is
	// both a branch and a tag. The remote has branch "main" and tag "rel".
	for _, ref := range []plumbing.ReferenceName{"refs/heads/work", "refs/heads/main", "refs/tags/v1", "refs/heads/both", "refs/tags/both"} {
		if err := lr.Storer.SetReference(plumbing.NewHashReference(ref, head)); err != nil {
			t.Fatal(err)
		}
	}
	if err := lr.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/work")); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []plumbing.ReferenceName{"refs/heads/main", "refs/tags/rel"} {
		if err := lr.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec("refs/heads/work:" + ref.String())}}); err != nil {
			t.Fatal(err)
		}
	}

	tr := newEmbeddedTransport(lr, nil)
	cases := []struct{ in, want, err string }{
		{in: "HEAD:main", want: "refs/heads/work:refs/heads/main"},
		{in: "+HEAD:main", want: "+refs/heads/work:refs/heads/main"},
		{in: "HEAD:refs/heads/main", want: "refs/heads/work:refs/heads/main"},
		{in: "main:main", want: "refs/heads/main:refs/heads/main"},
		{in: "main", want: "refs/heads/main:refs/heads/main"},
		{in: "HEAD", want: "refs/heads/work:refs/heads/work"},
		{in: "work:feature", want: "refs/heads/work:refs/heads/feature"},
		{in: "v1", want: "refs/tags/v1:refs/tags/v1"},
		{in: "v1:v2", want: "refs/tags/v1:refs/tags/v2"},
		{in: "work:rel", want: "refs/heads/work:refs/tags/rel"},
		{in: "both:x", err: "both a branch and a tag"},
		{in: "nope:main", err: "matches no local branch or tag"},
	}
	for _, c := range cases {
		got, err := tr.pushRefSpec("origin", c.in, nil)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("pushRefSpec(%q) error = %v, want %q", c.in, err, c.err)
			}
			continue
		}
		if err != nil || string(got) != c.want {
			t.Errorf("pushRefSpec(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}
