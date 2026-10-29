package upstream

import (
	"io"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

var sig = object.Signature{Name: "t", Email: "t@t", When: time.Unix(1700000000, 0).UTC()}

// mk writes a commit with exactly these files on top of parents.
func mk(t *testing.T, repo *git.Repository, files map[string]string, msg string, parents ...plumbing.Hash) plumbing.Hash {
	t.Helper()
	flat := map[string]object.TreeEntry{}
	for path, body := range files {
		obj := repo.Storer.NewEncodedObject()
		obj.SetType(plumbing.BlobObject)
		w, _ := obj.Writer()
		w.Write([]byte(body))
		w.Close()
		h, err := repo.Storer.SetEncodedObject(obj)
		if err != nil {
			t.Fatal(err)
		}
		flat[path] = object.TreeEntry{Name: path, Mode: filemode.Regular, Hash: h}
	}
	tree, err := gitstate.WriteTreeFromFlat(repo, flat)
	if err != nil {
		t.Fatal(err)
	}
	h, err := writeCommit(repo, &object.Commit{Author: sig, Committer: sig, Message: msg, TreeHash: tree, ParentHashes: parents})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func file(t *testing.T, repo *git.Repository, commit plumbing.Hash, path string) string {
	t.Helper()
	c, _ := repo.CommitObject(commit)
	f, err := c.File(path)
	if err != nil {
		return "<missing>"
	}
	r, _ := f.Reader()
	defer r.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// fork: upstream v1 → v2 (bumps VERSION, edits app.go); ours: v1 + a patch to the Dockerfile.
func fork(t *testing.T, ourPatch map[string]string) (repo *git.Repository, v1, v2, ours plumbing.Hash) {
	repo, _ = git.Init(memory.NewStorage(), nil)
	base := map[string]string{"VERSION": "1.0.0\n", "Dockerfile": "FROM alpine\nRUN apk add nut\nCMD [\"upsd\"]\n", "app.go": "package main\n\nfunc main() {}\n"}
	v1 = mk(t, repo, base, "v1")
	up := map[string]string{"VERSION": "1.1.0\n", "Dockerfile": base["Dockerfile"], "app.go": "package main\n\nfunc main() { run() }\n"}
	v2 = mk(t, repo, up, "v2", v1)
	mine := map[string]string{}
	for k, v := range base {
		mine[k] = v
	}
	for k, v := range ourPatch {
		mine[k] = v
	}
	ours = mk(t, repo, mine, "our patch", v1)
	return
}

func TestPlanSync_RebaseCarriesOurPatchOntoTheRelease(t *testing.T) {
	repo, _, v2, ours := fork(t, map[string]string{"Dockerfile": "FROM alpine\nRUN apk add nut tini\nCMD [\"upsd\"]\n"})
	plan, err := PlanSync(repo, ours, v2, SyncOptions{Strategy: config.SyncRebase, Sig: sig, MergeMessage: ""})
	if err != nil || plan.Result != SyncSynced || !plan.Rewrites {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	c, _ := repo.CommitObject(plan.NewHead)
	if c.ParentHashes[0] != v2 || c.Message != "our patch" || c.Author.Name != "t" {
		t.Fatalf("the replayed commit keeps ours and sits on upstream's release: %+v", c)
	}
	if file(t, repo, plan.NewHead, "VERSION") != "1.1.0\n" || !strings.Contains(file(t, repo, plan.NewHead, "Dockerfile"), "tini") || !strings.Contains(file(t, repo, plan.NewHead, "app.go"), "run()") {
		t.Fatal("the result holds upstream's release and our patch")
	}
	again, _ := PlanSync(repo, ours, v2, SyncOptions{Strategy: config.SyncRebase, Sig: sig, MergeMessage: ""})
	if again.NewHead != plan.NewHead {
		t.Fatal("replaying the same history onto the same release must give the same hash")
	}
	if inSync, _ := PlanSync(repo, plan.NewHead, v2, SyncOptions{Strategy: config.SyncRebase, Sig: sig, MergeMessage: ""}); inSync.Result != SyncInSync {
		t.Fatalf("after the rebase we are in sync: %+v", inSync)
	}
}

func TestPlanSync_MergeJoinsWithoutRewriting(t *testing.T) {
	repo, _, v2, ours := fork(t, map[string]string{"Dockerfile": "FROM alpine\nRUN apk add nut tini\nCMD [\"upsd\"]\n"})
	plan, err := PlanSync(repo, ours, v2, SyncOptions{Strategy: config.SyncMerge, Sig: sig, MergeMessage: "Merge upstream core v2\n\nUpstream-Rev: x\n"})
	if err != nil || plan.Result != SyncSynced || plan.Rewrites {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	c, _ := repo.CommitObject(plan.NewHead)
	if len(c.ParentHashes) != 2 || c.ParentHashes[0] != ours || c.ParentHashes[1] != v2 || !strings.Contains(c.Message, "Upstream-Rev") {
		t.Fatalf("merge commit = %+v", c)
	}
}

func TestPlanSync_ConflictProducesNothing(t *testing.T) {
	repo, _, v2, ours := fork(t, map[string]string{"VERSION": "1.0.0-ours\n"})
	for _, s := range []string{config.SyncRebase, config.SyncMerge} {
		plan, err := PlanSync(repo, ours, v2, SyncOptions{Strategy: s, Sig: sig, MergeMessage: "m"})
		if err != nil || plan.Result != SyncConflict || strings.Join(plan.Conflicts, ",") != "VERSION" || !plan.NewHead.IsZero() {
			t.Fatalf("%s: plan = %+v, %v", s, plan, err)
		}
	}
}

func TestPlanSync_PatchUpstreamAlreadyHasIsDropped(t *testing.T) {
	repo, _, v2, ours := fork(t, map[string]string{"app.go": "package main\n\nfunc main() { run() }\n"})
	plan, _ := PlanSync(repo, ours, v2, SyncOptions{Strategy: config.SyncRebase, Sig: sig, MergeMessage: ""})
	if plan.Result != SyncSynced || plan.NewHead != v2 {
		t.Fatalf("a patch upstream took is dropped; we land on the release itself: %+v", plan)
	}
	if len(plan.Dropped) != 1 {
		t.Fatalf("the dropped fork commit should be recorded, got %v", plan.Dropped)
	}
}

func TestPlanSync_FastForwardAndMirror(t *testing.T) {
	repo, v1, v2, ours := fork(t, map[string]string{"Dockerfile": "x\n"})
	for _, s := range []string{config.SyncRebase, config.SyncMerge, config.SyncMirror} {
		if plan, _ := PlanSync(repo, v1, v2, SyncOptions{Strategy: s, Sig: sig, MergeMessage: "m"}); plan.Result != SyncSynced || plan.NewHead != v2 || plan.Rewrites {
			t.Fatalf("%s: nothing of ours on top is a fast-forward: %+v", s, plan)
		}
	}
	if plan, _ := PlanSync(repo, ours, v2, SyncOptions{Strategy: config.SyncMirror, Sig: sig, MergeMessage: "m"}); plan.Result != SyncConflict || !strings.Contains(plan.Reason, "diverged") {
		t.Fatalf("a diverged mirror is kept and reported: %+v", plan)
	}
}

func TestPlanSync_RebaseRefusesMergeHistory(t *testing.T) {
	repo, v1, v2, ours := fork(t, map[string]string{"Dockerfile": "x\n"})
	side := mk(t, repo, map[string]string{"VERSION": "1.0.0\n", "Dockerfile": "x\n", "app.go": "package main\n\nfunc main() {}\n", "NOTES": "n\n"}, "side", v1)
	merged := mk(t, repo, map[string]string{"VERSION": "1.0.0\n", "Dockerfile": "x\n", "app.go": "package main\n\nfunc main() {}\n", "NOTES": "n\n"}, "merge", ours, side)
	if plan, _ := PlanSync(repo, merged, v2, SyncOptions{Strategy: config.SyncRebase, Sig: sig, MergeMessage: ""}); plan.Result != SyncConflict || !strings.Contains(plan.Reason, "use strategy: merge") {
		t.Fatalf("plan = %+v", plan)
	}
}
