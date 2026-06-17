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

// When an uncommitted edit touches a path the rebase also changes, the replay
// aborts BEFORE any mutation — the worktree is intact, the change is not lost.
func TestReplay_AutostashConflictAbortsCleanly(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "upstream.txt", "from remote")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")

	// Uncommitted edit to local.txt — which the local commit (the rebase) also touches.
	writeTestFile(t, r.localDir, "local.txt", "conflicting wip")

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	err := Replay(session)
	if err == nil || !strings.Contains(err.Error(), "local.txt") {
		t.Fatalf("expected an abort naming local.txt, got: %v", err)
	}
	// Worktree untouched — the operator's uncommitted change is preserved, not lost.
	got, rerr := os.ReadFile(filepath.Join(r.localDir, "local.txt"))
	if rerr != nil || string(got) != "conflicting wip" {
		t.Errorf("conflict abort must leave the worktree intact: local.txt = %q (%v)", got, rerr)
	}
}
