package commit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A dirty worktree must NOT block a push (git push never touches the worktree);
// the replay autostashes uncommitted edits and restores them after the rebase.
func TestReplay_AutostashPreservesUncommitted(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "upstream.txt", "from remote") // divergence → rebase required
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")

	// Uncommitted edit to a tracked file the rebase does NOT touch (the seed
	// README.md). `git push` would leave it alone; the replay must too.
	writeTestFile(t, r.localDir, "README.md", "WORK IN PROGRESS")

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := Replay(session); err != nil {
		t.Fatalf("replay must not refuse a dirty worktree: %v", err)
	}

	// The uncommitted edit survived the rebase.
	got, err := os.ReadFile(filepath.Join(r.localDir, "README.md"))
	if err != nil || string(got) != "WORK IN PROGRESS" {
		t.Errorf("autostash lost the uncommitted change: README.md = %q (%v)", got, err)
	}
	// And the rebase still happened — the local commit pushes onto upstream.
	if err := session.Push("origin", "", false); err != nil {
		t.Fatalf("push after replay: %v", err)
	}
	assertRemoteHasFile(t, r.remoteDir, "local.txt", "local content")
}

// An uncommitted edit to a path upstream changes is a conflict: the checkout would
// overwrite it, so the replay aborts BEFORE any mutation with the worktree intact.
func TestReplay_AutostashConflictAbortsCleanly(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "README.md", "upstream readme")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "README.md", "conflicting wip")

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	err := Replay(session)
	if err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("expected an abort naming README.md, got: %v", err)
	}
	got, rerr := os.ReadFile(filepath.Join(r.localDir, "README.md"))
	if rerr != nil || string(got) != "conflicting wip" {
		t.Errorf("conflict abort must leave the worktree intact: README.md = %q (%v)", got, rerr)
	}
}

// An uncommitted edit to a path only the local commits touched is kept, as with
// `git rebase --autostash`: the replayed head carries the same content there, so the
// checkout never writes it.
func TestReplay_KeepsEditToLocallyCommittedPath(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "upstream.txt", "from remote")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "local.txt", "wip on top")

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "local.txt")); string(got) != "wip on top" {
		t.Errorf("uncommitted edit lost: local.txt = %q", got)
	}
}
