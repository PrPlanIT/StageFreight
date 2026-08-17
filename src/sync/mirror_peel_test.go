package sync

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// annotatedTagRepo builds a repo on main with one commit and an ANNOTATED tag at it,
// returning the dir and the commit hash the tag ultimately names.
func annotatedTagRepo(t *testing.T, tag string) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("f"); err != nil {
		t.Fatal(err)
	}
	c, err := wt.Commit("c", &git.CommitOptions{Author: testSig()})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := repo.CreateTag(tag, c, &git.CreateTagOptions{Tagger: testSig(), Message: "release " + tag}); err != nil {
		t.Fatalf("annotated tag: %v", err)
	}
	return dir, c.String()
}

func mirrorClone(t *testing.T, source string) *git.Repository {
	t.Helper()
	bare, err := git.PlainClone(t.TempDir(), true, &git.CloneOptions{URL: source, Mirror: true})
	if err != nil {
		t.Fatalf("mirror clone: %v", err)
	}
	return bare
}

// A — collectLocalRefs must peel an annotated tag to its target commit, not store the
// tag-object hash (which reads as a false divergence against a lightweight tag).
func TestCollectLocalRefs_PeelsAnnotatedTag(t *testing.T) {
	source, commit := annotatedTagRepo(t, "v2")
	bare := mirrorClone(t, source)

	refs, err := collectLocalRefs(bare)
	if err != nil {
		t.Fatal(err)
	}
	if got := refs["refs/tags/v2"]; got != commit {
		t.Errorf("refs/tags/v2 = %q, want peeled commit %q", got, commit)
	}
}

// A — listRemoteRefs must peel a remote annotated tag to its commit via AppendPeeled.
func TestListRemoteRefs_PeelsAnnotatedTag(t *testing.T) {
	source, commit := annotatedTagRepo(t, "v3")
	remote := setupBareRemote(t)
	bare := mirrorClone(t, source)
	if _, err := bare.CreateRemote(&gitconfig.RemoteConfig{Name: "mirror", URLs: []string{remote}}); err != nil {
		t.Fatal(err)
	}
	// Push heads + the annotated tag so the remote holds the tag OBJECT.
	if err := bare.Push(&git.PushOptions{RemoteName: "mirror", RefSpecs: []gitconfig.RefSpec{
		"refs/heads/main:refs/heads/main", "refs/tags/v3:refs/tags/v3",
	}}); err != nil && err != git.NoErrAlreadyUpToDate {
		t.Fatalf("seed remote: %v", err)
	}

	remoteRefs, err := listRemoteRefs(t.Context(), bare, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := remoteRefs["refs/tags/v3"]; got != commit {
		t.Errorf("remote refs/tags/v3 = %q, want peeled commit %q", got, commit)
	}
}

// A — an annotated tag locally and a lightweight tag on the mirror at the SAME commit
// must read as in-sync: no divergence, no refspec.
func TestMirror_AnnotatedVsLightweightSameCommit_NotDiverged(t *testing.T) {
	source, commit := annotatedTagRepo(t, "v2") // local: annotated v2 @ commit
	remote := setupBareRemote(t)
	bare := mirrorClone(t, source)
	if _, err := bare.CreateRemote(&gitconfig.RemoteConfig{Name: "mirror", URLs: []string{remote}}); err != nil {
		t.Fatal(err)
	}
	// Seed the remote with main only, then create a LIGHTWEIGHT v2 at the same commit.
	if err := bare.Push(&git.PushOptions{RemoteName: "mirror", RefSpecs: []gitconfig.RefSpec{
		"refs/heads/main:refs/heads/main",
	}}); err != nil && err != git.NoErrAlreadyUpToDate {
		t.Fatalf("seed main: %v", err)
	}
	rrepo := openRepo(t, remote)
	if _, err := rrepo.CreateTag("v2", plumbing.NewHash(commit), nil); err != nil { // lightweight
		t.Fatalf("lightweight tag on remote: %v", err)
	}

	localRefs, err := collectLocalRefs(bare)
	if err != nil {
		t.Fatal(err)
	}
	remoteRefs, err := listRemoteRefs(t.Context(), bare, nil)
	if err != nil {
		t.Fatal(err)
	}
	if localRefs["refs/tags/v2"] != commit || remoteRefs["refs/tags/v2"] != commit {
		t.Fatalf("both sides should peel to %s: local=%s remote=%s",
			commit, localRefs["refs/tags/v2"], remoteRefs["refs/tags/v2"])
	}

	tags := &config.FacetSpec{Scope: "all"}
	plan := buildPushRefSpecs(localRefs, remoteRefs, nil, tags, RefContext{}, nil, nil)
	for _, d := range plan.diverged {
		if d == "refs/tags/v2" {
			t.Error("annotated-vs-lightweight same commit must NOT be diverged")
		}
	}
	for _, s := range plan.specs {
		if strings.Contains(s.String(), "refs/tags/v2") {
			t.Errorf("v2 is in sync — expected no refspec, got %q", s)
		}
	}
}

// B — a rolling alias force-updates when diverged; an immutable version tag does not.
func TestBuildPushRefSpecs_RollingAliasForced(t *testing.T) {
	local := map[string]string{"refs/tags/latest": "new", "refs/tags/v1.0.0": "new"}
	remote := map[string]string{"refs/tags/latest": "old", "refs/tags/v1.0.0": "old"}
	tags := &config.FacetSpec{Scope: "all"}
	rolling := map[string]bool{"latest": true}

	plan := buildPushRefSpecs(local, remote, nil, tags, RefContext{}, rolling, nil)
	joined := func() string {
		var out []string
		for _, s := range plan.specs {
			out = append(out, s.String())
		}
		return strings.Join(out, " ")
	}()

	if !strings.Contains(joined, "+refs/tags/latest:refs/tags/latest") {
		t.Errorf("rolling alias latest must be force-pushed; specs=%q", joined)
	}
	if strings.Contains(joined, "+refs/tags/v1.0.0") {
		t.Errorf("immutable v1.0.0 must NOT be forced; specs=%q", joined)
	}
	if !strings.Contains(joined, "refs/tags/v1.0.0:refs/tags/v1.0.0") {
		t.Errorf("immutable v1.0.0 should still push non-force (keep-divergent); specs=%q", joined)
	}
	for _, d := range plan.diverged {
		if d == "refs/tags/latest" {
			t.Error("a force-updated rolling alias must not be reported as diverged")
		}
	}
	if !containsStr(plan.diverged, "refs/tags/v1.0.0") {
		t.Errorf("immutable v1.0.0 (differing, non-force) should be reported diverged; diverged=%v", plan.diverged)
	}
}

// D — a rejection carrying kept-divergent refs classifies as MirrorDiverged with a clean
// message; a non-diverged rejection falls through to error classification (and go-git's
// "object not found" no longer reads as remote_not_found).
func TestClassifyPushFailure(t *testing.T) {
	reason, msg := classifyPushFailure(errors.New("object not found"), refPushPlan{diverged: []string{"refs/tags/latest", "refs/heads/main"}})
	if reason != MirrorDiverged {
		t.Errorf("reason = %s, want %s", reason, MirrorDiverged)
	}
	if !strings.Contains(msg, "diverged, kept: refs/tags/latest, refs/heads/main") || !strings.Contains(msg, "set sync force") {
		t.Errorf("message = %q, want the diverged-kept wording", msg)
	}

	reason2, _ := classifyPushFailure(errors.New("repository not found"), refPushPlan{})
	if reason2 != MirrorRemoteNotFound {
		t.Errorf("non-diverged reason = %s, want %s", reason2, MirrorRemoteNotFound)
	}
	reason3, _ := classifyPushFailure(errors.New("object not found"), refPushPlan{})
	if reason3 == MirrorRemoteNotFound {
		t.Error(`"object not found" without divergence must not classify as remote_not_found`)
	}
}

// twoCommitRepo builds a repo on main with two commits (parent → tip) and returns the
// dir plus both hashes.
func twoCommitRepo(t *testing.T) (dir, parent, tip string) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "f"), []byte("1"), 0o644)
	_, _ = wt.Add("f")
	a, err := wt.Commit("a", &git.CommitOptions{Author: testSig()})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "f"), []byte("2"), 0o644)
	_, _ = wt.Add("f")
	b, err := wt.Commit("b", &git.CommitOptions{Author: testSig()})
	if err != nil {
		t.Fatal(err)
	}
	return dir, a.String(), b.String()
}

// E — a mirror ref behind by an ancestor is a fast-forward: an Update (non-force), and it
// does NOT appear in the diverged set — exercising the real ancestryChecker over the bare
// repo.
func TestMirror_FastForwardMainIsUpdateNotDiverged(t *testing.T) {
	source, parent, tip := twoCommitRepo(t) // main @ tip, whose parent is `parent`
	remote := setupBareRemote(t)
	bare := mirrorClone(t, source) // holds both commits
	if _, err := bare.CreateRemote(&gitconfig.RemoteConfig{Name: "mirror", URLs: []string{remote}}); err != nil {
		t.Fatal(err)
	}
	// Seed the remote with full history, then rewind its main to the ancestor commit.
	if err := bare.Push(&git.PushOptions{RemoteName: "mirror", RefSpecs: []gitconfig.RefSpec{
		"refs/heads/main:refs/heads/main",
	}}); err != nil && err != git.NoErrAlreadyUpToDate {
		t.Fatalf("seed: %v", err)
	}
	rrepo := openRepo(t, remote)
	if err := rrepo.Storer.SetReference(plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("main"), plumbing.NewHash(parent))); err != nil {
		t.Fatal(err)
	}

	localRefs, err := collectLocalRefs(bare)
	if err != nil {
		t.Fatal(err)
	}
	remoteRefs, err := listRemoteRefs(t.Context(), bare, nil)
	if err != nil {
		t.Fatal(err)
	}
	if localRefs["refs/heads/main"] != tip || remoteRefs["refs/heads/main"] != parent {
		t.Fatalf("setup: local main=%s (want %s), remote main=%s (want %s)",
			localRefs["refs/heads/main"], tip, remoteRefs["refs/heads/main"], parent)
	}

	branches := &config.FacetSpec{Scope: "all"}
	plan := buildPushRefSpecs(localRefs, remoteRefs, branches, nil, RefContext{}, nil, ancestryChecker(bare))
	if containsStr(plan.diverged, "refs/heads/main") {
		t.Errorf("a fast-forward main must NOT be diverged; diverged=%v", plan.diverged)
	}
	var joined []string
	for _, s := range plan.specs {
		joined = append(joined, s.String())
	}
	all := strings.Join(joined, " ")
	if !strings.Contains(all, "refs/heads/main:refs/heads/main") || strings.Contains(all, "+refs/heads/main") {
		t.Errorf("fast-forward main must push non-force; specs=%q", all)
	}
}

// E — a mirror commit absent from local history is a divergence: the ancestry closure
// returns false when the commit is unresolvable locally, so keep-divergent holds.
func TestMirror_AbsentRemoteCommitIsDiverged(t *testing.T) {
	source, _, tip := twoCommitRepo(t)
	remote := setupBareRemote(t)
	bare := mirrorClone(t, source)
	if _, err := bare.CreateRemote(&gitconfig.RemoteConfig{Name: "mirror", URLs: []string{remote}}); err != nil {
		t.Fatal(err)
	}
	if err := bare.Push(&git.PushOptions{RemoteName: "mirror", RefSpecs: []gitconfig.RefSpec{
		"refs/heads/main:refs/heads/main",
	}}); err != nil && err != git.NoErrAlreadyUpToDate {
		t.Fatalf("seed: %v", err)
	}
	// Point remote main at a commit that does not exist in local history.
	absent := plumbing.NewHash("0123456789012345678901234567890123456789")
	rrepo := openRepo(t, remote)
	if err := rrepo.Storer.SetReference(plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("main"), absent)); err != nil {
		t.Fatal(err)
	}

	localRefs, _ := collectLocalRefs(bare)
	remoteRefs, _ := listRemoteRefs(t.Context(), bare, nil)
	if localRefs["refs/heads/main"] != tip {
		t.Fatalf("setup: local main=%s want %s", localRefs["refs/heads/main"], tip)
	}
	branches := &config.FacetSpec{Scope: "all"}
	plan := buildPushRefSpecs(localRefs, remoteRefs, branches, nil, RefContext{}, nil, ancestryChecker(bare))
	if !containsStr(plan.diverged, "refs/heads/main") {
		t.Errorf("an absent remote commit must be diverged; diverged=%v", plan.diverged)
	}
}

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
