package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream"
)

// commitFile writes body to path in the worktree and commits it.
func commitFile(t *testing.T, repo *git.Repository, dir, path, body, msg string) plumbing.Hash {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	wt.Add(path)
	h, err := wt.Commit(msg, &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@t", When: time.Unix(1700000000, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// syncWorld builds an upstream (v1.0.0, then v1.1.0 which changes app.txt), our origin (forked
// at v1.0.0 with our patch to ours.txt — or, conflicting, to app.txt), and a CI checkout of it.
func syncWorld(t *testing.T, conflicting bool) (upURL, originDir, work string, head plumbing.Hash, v11 plumbing.Hash) {
	t.Helper()
	root := t.TempDir()
	upDir := filepath.Join(root, "upstream")
	up, _ := git.PlainInit(upDir, false)
	commitFile(t, up, upDir, "app.txt", "one\ntwo\nthree\n", "upstream one")
	v10 := commitFile(t, up, upDir, "VERSION", "1.0.0\n", "release 1.0.0")
	up.CreateTag("v1.0.0", v10, nil)

	originDir = filepath.Join(root, "origin.git")
	if _, err := git.PlainClone(originDir, true, &git.CloneOptions{URL: upDir}); err != nil {
		t.Fatal(err)
	}
	work = filepath.Join(root, "work")
	wr, err := git.PlainClone(work, false, &git.CloneOptions{URL: originDir})
	if err != nil {
		t.Fatal(err)
	}
	if conflicting {
		head = commitFile(t, wr, work, "app.txt", "one\nOURS\nthree\n", "our patch")
	} else {
		head = commitFile(t, wr, work, "ours.txt", "patch\n", "our patch")
	}
	if err := wr.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{"refs/heads/master:refs/heads/master"}}); err != nil {
		t.Fatal(err)
	}

	commitFile(t, up, upDir, "app.txt", "one\nTWO\nthree\n", "upstream two")
	v11 = commitFile(t, up, upDir, "VERSION", "1.1.0\n", "release 1.1.0")
	if _, err := up.CreateTag("v1.1.0", v11, &git.CreateTagOptions{Message: "1.1.0", Tagger: &object.Signature{Name: "u", Email: "u@u", When: time.Unix(1700000100, 0)}}); err != nil {
		t.Fatal(err)
	}
	return "file://" + upDir, originDir, work, head, v11
}

func syncCfg(upURL, strategy, onConflict string) *config.Config {
	return &config.Config{Upstream: config.UpstreamConfig{
		Sources: config.OrderedUpstreamSources{{ID: "core", Git: upURL}},
		Sync:    config.UpstreamSync{From: "core", Strategy: strategy, OnConflict: onConflict, Shadow: "upstream"},
	}}
}

func originHead(t *testing.T, originDir, branch string) *object.Commit {
	t.Helper()
	o, _ := git.PlainOpen(originDir)
	ref, err := o.Reference(plumbing.NewBranchReferenceName(branch), true)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := o.CommitObject(ref.Hash())
	return c
}

func TestSyncUpstream_RebaseOntoNewRelease(t *testing.T) {
	upURL, originDir, work, head, v11 := syncWorld(t, false)
	ciCtx := &ci.CIContext{Provider: "gitlab", Branch: "master", DefaultBranch: "master", SHA: head.String()}
	pushed, refuse := syncUpstream(t.Context(), syncCfg(upURL, "", ""), ciCtx, work)
	if !pushed || refuse != "" {
		t.Fatalf("pushed=%v refuse=%q", pushed, refuse)
	}
	c := originHead(t, originDir, "master")
	if c.Message != "our patch" || c.ParentHashes[0] != v11 {
		t.Fatalf("origin master should be our patch on top of v1.1.0: %s parent %v", c.Message, c.ParentHashes)
	}
	if shadow := originHead(t, originDir, "upstream"); shadow.Hash != v11 {
		t.Fatalf("shadow branch = %s, want upstream's v1.1.0", shadow.Hash)
	}
	st, _ := cistate.ReadState(work)
	if s := st.GetSubsystem("sync"); s == nil || s.Results["target"] != "v1.1.0" || s.Results["result"] != "synced" {
		t.Fatalf("sync facts = %+v", s)
	}

	// release: auto for a synced history fork: the built commit contains upstream's v1.1.0.
	cfg := syncCfg(upURL, "", "")
	srcs, _ := upstream.Sources(cfg.Upstream)
	wr0, _ := git.PlainOpen(work)
	wr0.Fetch(&git.FetchOptions{RemoteName: "origin", Force: true})
	if tag, reason := autoReleaseTag(t.Context(), work, cfg.Upstream.Sources[0], srcs[0], c.Hash.String()); tag != "v1.1.0" {
		t.Fatalf("auto-release tag = %q (%s), want v1.1.0", tag, reason)
	}

	// The next run, on the synced commit, finds nothing to do.
	ciCtx.SHA = c.Hash.String()
	wr, _ := git.PlainOpen(work)
	wr.Fetch(&git.FetchOptions{RemoteName: "origin", Force: true})
	if pushed, _ := syncUpstream(t.Context(), syncCfg(upURL, "", ""), ciCtx, work); pushed {
		t.Fatal("in sync: nothing pushed")
	}
}

func TestSyncUpstream_MergeOntoNewRelease(t *testing.T) {
	upURL, originDir, work, head, v11 := syncWorld(t, false)
	ciCtx := &ci.CIContext{Provider: "gitlab", Branch: "master", DefaultBranch: "master", SHA: head.String()}
	if pushed, _ := syncUpstream(t.Context(), syncCfg(upURL, "merge", ""), ciCtx, work); !pushed {
		t.Fatal("merge should push")
	}
	c := originHead(t, originDir, "master")
	if len(c.ParentHashes) != 2 || c.ParentHashes[0] != head || c.ParentHashes[1] != v11 || !strings.Contains(c.Message, "Upstream-Rev: "+v11.String()) {
		t.Fatalf("merge commit = %q parents %v", c.Message, c.ParentHashes)
	}
}

func TestSyncUpstream_ConflictChangesNothing(t *testing.T) {
	upURL, originDir, work, head, _ := syncWorld(t, true)
	ciCtx := &ci.CIContext{Provider: "gitlab", Branch: "master", DefaultBranch: "master", SHA: head.String()}
	pushed, refuse := syncUpstream(t.Context(), syncCfg(upURL, "", "notify"), ciCtx, work)
	if pushed || refuse != "" || originHead(t, originDir, "master").Hash != head {
		t.Fatalf("a conflict must leave our branch alone: pushed=%v refuse=%q", pushed, refuse)
	}
	st, _ := cistate.ReadState(work)
	if s := st.GetSubsystem("sync"); s == nil || s.Results["conflicts"] != "app.txt" || s.Results[cistate.SignalsKey] != config.SignalSyncConflict {
		t.Fatalf("sync facts = %+v", s)
	}
	if _, refuse := syncUpstream(t.Context(), syncCfg(upURL, "", "fail"), ciCtx, work); !strings.Contains(refuse, "app.txt") {
		t.Fatalf("on_conflict: fail refuses the run: %q", refuse)
	}
}

func TestSyncUpstream_OffTheDefaultBranchDoesNothing(t *testing.T) {
	upURL, _, work, head, _ := syncWorld(t, false)
	ciCtx := &ci.CIContext{Provider: "gitlab", Branch: "feature", DefaultBranch: "master", SHA: head.String()}
	if pushed, _ := syncUpstream(t.Context(), syncCfg(upURL, "", ""), ciCtx, work); pushed {
		t.Fatal("sync binds to the default branch")
	}
}
