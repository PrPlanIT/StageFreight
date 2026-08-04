package commit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// replayFixture builds the motivating history shape in a temp repo:
//
//	base ─── local   (scribe commit on the tagged parent: badges + README)
//	   └──── tip     (the dev pipeline's own docs commit, already on main)
//
// Both sides touch overlap.md; local also touches its own files.
func replayFixture(t *testing.T) (repo *git.Repository, base, local, tip plumbing.Hash) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) plumbing.Hash {
		t.Helper()
		h, err := wt.Commit(msg, &git.CommitOptions{
			Author: &object.Signature{Name: "test", Email: "test@example.com"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	write("README.md", "readme v1")
	write("overlap.md", "base overlap")
	write("docs/keep.md", "kept forever")
	write("stale.md", "to be deleted by local")
	base = commit("base")

	// local: the scribe commit on the tagged parent.
	write("badges/release.svg", "svg v0.8.0")
	write("README.md", "readme with release badge")
	write("overlap.md", "local overlap render")
	if _, err := wt.Remove("stale.md"); err != nil {
		t.Fatal(err)
	}
	local = commit("docs: refresh generated docs and badges")

	// tip: rewind to base, advance main the way the dev pipeline did.
	if err := wt.Reset(&git.ResetOptions{Commit: base, Mode: git.HardReset}); err != nil {
		t.Fatal(err)
	}
	write("overlap.md", "tip overlap render")
	write("docs/newer.md", "tip-only file")
	tip = commit("docs: dev pipeline render")

	return repo, base, local, tip
}

func treeContent(t *testing.T, repo *git.Repository, commit plumbing.Hash, path string) (string, bool) {
	t.Helper()
	c, err := repo.CommitObject(commit)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := c.Tree()
	if err != nil {
		t.Fatal(err)
	}
	f, err := tree.File(path)
	if err != nil {
		return "", false
	}
	s, err := f.Contents()
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}

// TestReplayCommitOntoTip_Motivating pins the tag-pipeline scenario: the scribe
// commit rebuilds onto the advanced tip, its own paths win (last-writer on the
// overlap), tip-only content is preserved, and identity/message carry over.
func TestReplayCommitOntoTip_Motivating(t *testing.T) {
	repo, _, local, tip := replayFixture(t)

	newHash, err := replayCommitOntoTip(repo, local, tip)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	newC, err := repo.CommitObject(newHash)
	if err != nil {
		t.Fatal(err)
	}
	if newC.NumParents() != 1 {
		t.Fatalf("parents = %d", newC.NumParents())
	}
	if p, _ := newC.Parent(0); p.Hash != tip {
		t.Errorf("parent = %s, want tip %s", short(p.Hash), short(tip))
	}
	if !strings.Contains(newC.Message, "refresh generated docs") {
		t.Errorf("message not carried: %q", newC.Message)
	}

	for path, want := range map[string]string{
		"badges/release.svg": "svg v0.8.0",                // local's new file
		"README.md":          "readme with release badge", // local's modify
		"overlap.md":         "local overlap render",      // overlap → replayed commit wins
		"docs/newer.md":      "tip-only file",             // tip content preserved
		"docs/keep.md":       "kept forever",              // untouched everywhere
	} {
		got, ok := treeContent(t, repo, newHash, path)
		if !ok {
			t.Errorf("%s: missing", path)
			continue
		}
		if got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
	if _, ok := treeContent(t, repo, newHash, "stale.md"); ok {
		t.Error("stale.md: local's deletion did not replay")
	}
}

// TestReplayCommitOntoTip_ParentIsTip pins the raced-push fast path: nothing to
// replay, the original commit comes back unchanged.
func TestReplayCommitOntoTip_ParentIsTip(t *testing.T) {
	repo, base, local, _ := replayFixture(t)

	got, err := replayCommitOntoTip(repo, local, base)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got != local {
		t.Errorf("parent==tip must return the commit unchanged: got %s want %s", short(got), short(local))
	}
}

// TestReplayCommitOntoTip_DeleteAbsentUpstream pins the shrunken-delta case: a
// deletion of a path the tip already lacks is a no-op, not a corruption.
func TestReplayCommitOntoTip_DeleteAbsentUpstream(t *testing.T) {
	repo, base, local, tip := replayFixture(t)

	// Advance the tip once more, deleting stale.md there too — now local's
	// deletion targets a path the tip already lacks.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Reset(&git.ResetOptions{Commit: tip, Mode: git.HardReset}); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Remove("stale.md"); err != nil {
		t.Fatal(err)
	}
	tip2, err := wt.Commit("tip removes stale.md as well", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = base

	newHash, err := replayCommitOntoTip(repo, local, tip2)
	if err != nil {
		t.Fatalf("replay with absent-upstream delete: %v", err)
	}
	if _, ok := treeContent(t, repo, newHash, "stale.md"); ok {
		t.Error("stale.md resurrected")
	}
	if got, _ := treeContent(t, repo, newHash, "overlap.md"); got != "local overlap render" {
		t.Errorf("overlap.md = %q", got)
	}
}

// TestVerifyContainment_RejectsForeignChange pins the corruption gate: a
// replayed delta touching a path the source never authorized raises
// ErrReplayCorruption (shared vocabulary with the worktree replay's gate).
func TestVerifyContainment_RejectsForeignChange(t *testing.T) {
	repo, _, local, tip := replayFixture(t)

	// Source delta of the real local commit…
	localC, _ := repo.CommitObject(local)
	parentC, _ := localC.Parent(0)
	pt, _ := parentC.Tree()
	lt, _ := localC.Tree()
	source, err := pt.Diff(lt)
	if err != nil {
		t.Fatal(err)
	}
	// …verified against a "replayed" delta that is actually the TIP's own delta
	// (touches overlap.md with a blob the source never produced, adds newer.md).
	tipC, _ := repo.CommitObject(tip)
	tt, _ := tipC.Tree()
	foreign, err := pt.Diff(tt)
	if err != nil {
		t.Fatal(err)
	}

	err = verifyContainment(source, foreign, "deadbeef")
	if err == nil {
		t.Fatal("foreign delta must be rejected")
	}
	if _, ok := err.(*ErrReplayCorruption); !ok {
		t.Fatalf("want ErrReplayCorruption, got %T: %v", err, err)
	}
}

// TestIsNonFastForwardErr pins the recoverable-refusal classifier across both
// transports' error texts; auth and policy failures are not replayable.
func TestIsNonFastForwardErr(t *testing.T) {
	cases := map[string]bool{
		"push to origin: non-fast-forward update: refs/heads/main": true,
		"! [rejected] main -> main (fetch first)":                  true,
		"remote: You are not allowed to push code":                 false,
		"authentication required":                                  false,
	}
	for msg, want := range cases {
		if got := isNonFastForwardErr(errString(msg)); got != want {
			t.Errorf("%q: %v, want %v", msg, got, want)
		}
	}
	if isNonFastForwardErr(nil) {
		t.Error("nil is not a refusal")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
