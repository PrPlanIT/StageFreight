package commit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// TestCommitPushFullFlowNoCorruption is incident investigation, not feature work:
// it drives the REAL GitBackend.Execute(--all --push) — stage → commit → engine
// sync → replay → push — against a divergent file:// remote, so the rebase-on-push
// path that produced 0deb152 actually runs. It covers a nested modify, two nested
// adds (different dirs), and a nested delete, and asserts the PUSHED tree carries
// full paths with no root-level basename corruption and no empty/lost commit.
func TestCommitPushFullFlowNoCorruption(t *testing.T) {
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	seedDir := filepath.Join(tmp, "seed")
	localDir := filepath.Join(tmp, "local")
	otherDir := filepath.Join(tmp, "other")
	remoteURL := "file://" + remote

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@t"}
	write := func(repoDir, rel, content string) {
		p := filepath.Join(repoDir, filepath.FromSlash(rel))
		if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(content), 0o644); e != nil {
			t.Fatal(e)
		}
	}

	// seed the remote with a base commit (files to later modify + delete)
	seedRepo, err := git.PlainInit(seedDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedRepo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{remoteURL}}); err != nil {
		t.Fatal(err)
	}
	swt, _ := seedRepo.Worktree()
	write(seedDir, "README.md", "base\n")
	write(seedDir, "src/gitstate/existing.go", "package x // v1\n")
	write(seedDir, "src/gitstate/todelete.go", "package x // bye\n")
	_ = swt.AddWithOptions(&git.AddOptions{All: true})
	if _, err := swt.Commit("base", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	if err := seedRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	// local + other clone the now-non-empty remote (upstream tracking configured)
	if _, err := git.PlainClone(localDir, false, &git.CloneOptions{URL: remoteURL}); err != nil {
		t.Fatalf("clone local: %v", err)
	}
	otherRepo, err := git.PlainClone(otherDir, false, &git.CloneOptions{URL: remoteURL})
	if err != nil {
		t.Fatalf("clone other: %v", err)
	}

	// other: divergent upstream commit, push
	owt, _ := otherRepo.Worktree()
	write(otherDir, "docs.md", "upstream docs\n")
	_ = owt.AddWithOptions(&git.AddOptions{All: true})
	if _, err := owt.Commit("divergent docs", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	if err := otherRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("divergent push: %v", err)
	}

	// local: nested modify + two nested adds + a nested delete
	write(localDir, "src/gitstate/existing.go", "package x // v2\n")
	write(localDir, "src/gitstate/new.go", "package x // new\n")
	write(localDir, "src/ci/handoff.go", "package ci\n")
	_ = os.Remove(filepath.Join(localDir, "src", "gitstate", "todelete.go"))

	// the REAL full flow
	backend := &GitBackend{RootDir: localDir}
	plan := &Plan{
		Type: "fix", Scope: "test", Summary: "nested change set",
		StageMode: StageAll,
		Push:      PushOptions{Enabled: true, Remote: "origin", RebaseOnDiverge: true},
	}
	res, err := backend.Execute(context.Background(), plan, true)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.SHA == "" {
		t.Fatal("expected a non-empty commit SHA")
	}
	if !res.Pushed {
		t.Fatalf("expected Pushed=true; sync=%+v", res.Sync)
	}

	// inspect the PUSHED top commit on the remote
	_ = otherRepo.Fetch(&git.FetchOptions{RemoteName: "origin"})
	ref, err := otherRepo.Reference(plumbing.NewRemoteReferenceName("origin", "master"), true)
	if err != nil {
		ref, err = otherRepo.Reference(plumbing.NewRemoteReferenceName("origin", "main"), true)
	}
	if err != nil {
		t.Fatalf("resolving pushed ref: %v", err)
	}
	top, err := otherRepo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, _ := top.Tree()
	got := map[string]bool{}
	_ = tree.Files().ForEach(func(f *object.File) error { got[f.Name] = true; return nil })

	for _, p := range []string{
		"README.md", "docs.md", "src/gitstate/existing.go",
		"src/gitstate/new.go", "src/ci/handoff.go",
	} {
		if !got[p] {
			t.Errorf("pushed tree missing %q; got %v", p, got)
		}
	}
	if got["src/gitstate/todelete.go"] {
		t.Error("deleted file still present in pushed tree")
	}
	for _, b := range []string{"existing.go", "new.go", "handoff.go", "todelete.go"} {
		if got[b] {
			t.Errorf("CORRUPTION: root basename %q in pushed tree", b)
		}
	}
	if roots, _ := filepath.Glob(filepath.Join(localDir, "*.go")); len(roots) > 0 {
		t.Errorf("CORRUPTION: root .go files in local worktree: %v", roots)
	}
}
