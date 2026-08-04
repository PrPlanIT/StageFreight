package commit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestRefspecPushReplaysOntoAdvancedMain is the end-to-end tag-pipeline shape:
// a DETACHED checkout commits scribe output and pushes HEAD:refs/heads/master
// while main has already advanced (the dev pipeline's own docs commit). The
// refspec path must recover via object replay and land the content on top of
// the advanced tip — the exact scenario the v0.8.0 release hit.
func TestRefspecPushReplaysOntoAdvancedMain(t *testing.T) {
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	seedDir := filepath.Join(tmp, "seed")
	ciDir := filepath.Join(tmp, "ci")
	otherDir := filepath.Join(tmp, "other")
	remoteURL := "file://" + remote

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@t"}
	write := func(repoDir, rel, content string) {
		t.Helper()
		p := filepath.Join(repoDir, filepath.FromSlash(rel))
		if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(content), 0o644); e != nil {
			t.Fatal(e)
		}
	}

	// Seed the remote: base commit — the commit the tag pipeline checks out.
	seedRepo, err := git.PlainInit(seedDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedRepo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{remoteURL}}); err != nil {
		t.Fatal(err)
	}
	swt, _ := seedRepo.Worktree()
	write(seedDir, "README.md", "readme v1\n")
	write(seedDir, "badges/release.svg", "svg v0.7.0\n")
	swt.AddWithOptions(&git.AddOptions{All: true})
	baseHash, err := swt.Commit("base (the tagged commit)", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}
	if err := seedRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	// Advance main from another clone — the dev pipeline's scribe commit.
	otherRepo, err := git.PlainClone(otherDir, false, &git.CloneOptions{URL: remoteURL})
	if err != nil {
		t.Fatal(err)
	}
	owt, _ := otherRepo.Worktree()
	write(otherDir, "badges/dev.svg", "dev badge\n")
	owt.AddWithOptions(&git.AddOptions{All: true})
	if _, err := owt.Commit("docs: dev pipeline render", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	if err := otherRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("advance push: %v", err)
	}

	// CI checkout: clone, then DETACH at the base commit (tag pipeline shape).
	ciRepo, err := git.PlainClone(ciDir, false, &git.CloneOptions{URL: remoteURL})
	if err != nil {
		t.Fatal(err)
	}
	cwt, _ := ciRepo.Worktree()
	if err := cwt.Checkout(&git.CheckoutOptions{Hash: baseHash}); err != nil {
		t.Fatalf("detach at base: %v", err)
	}

	// The tag pipeline's scribe commit on the detached HEAD.
	write(ciDir, "badges/release.svg", "svg v0.8.0\n")
	write(ciDir, "README.md", "readme with v0.8.0 badge\n")
	cwt.AddWithOptions(&git.AddOptions{All: true})
	scribeHash, err := cwt.Commit("docs: refresh generated docs and badges", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}

	// Push through the backend's refspec path — must replay, not fail.
	backend := &GitBackend{RootDir: ciDir}
	res, err := backend.Push(PushOptions{
		Enabled:         true,
		Remote:          "origin",
		Refspec:         "HEAD:refs/heads/master",
		RebaseOnDiverge: true,
	})
	if err != nil {
		t.Fatalf("refspec push with divergence: %v", err)
	}
	if !containsAction(res.ActionsExecuted, SyncRebase) || !containsAction(res.ActionsExecuted, SyncPush) {
		t.Errorf("actions = %v, want fetch+rebase+push", res.ActionsExecuted)
	}

	// The remote's main must now be: replayed scribe commit on top of the dev commit.
	remoteRepo, err := git.PlainOpen(remote)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := remoteRepo.Reference(plumbing.NewBranchReferenceName("master"), true)
	if err != nil {
		t.Fatal(err)
	}
	tipC, err := remoteRepo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if tipC.Message != "docs: refresh generated docs and badges" {
		t.Errorf("tip message = %q", tipC.Message)
	}
	if tipC.Hash == scribeHash {
		t.Error("tip is the original commit — replay did not rebuild it")
	}
	tree, _ := tipC.Tree()
	for path, want := range map[string]string{
		"badges/release.svg": "svg v0.8.0\n",               // tag pipeline's render
		"badges/dev.svg":     "dev badge\n",                // dev pipeline's content preserved
		"README.md":          "readme with v0.8.0 badge\n", // tag pipeline's README
	} {
		f, err := tree.File(path)
		if err != nil {
			t.Errorf("%s: missing on remote main", path)
			continue
		}
		got, _ := f.Contents()
		if got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}
