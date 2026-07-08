package gitstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInProgressOp(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	touch := func(name string) {
		if err := os.WriteFile(filepath.Join(gitDir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rm := func(name string) { _ = os.RemoveAll(filepath.Join(gitDir, name)) }

	if op := DetectInProgressOp(dir); op != "" {
		t.Fatalf("clean repo: want \"\", got %q", op)
	}

	touch("MERGE_HEAD")
	if op := DetectInProgressOp(dir); op != "merge" {
		t.Fatalf("merge: want merge, got %q", op)
	}
	rm("MERGE_HEAD")

	touch("CHERRY_PICK_HEAD")
	if op := DetectInProgressOp(dir); op != "cherry-pick" {
		t.Fatalf("cherry-pick: want cherry-pick, got %q", op)
	}
	rm("CHERRY_PICK_HEAD")

	if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if op := DetectInProgressOp(dir); op != "rebase" {
		t.Fatalf("rebase: want rebase, got %q", op)
	}

	// Linked worktree (.git is a file, not a dir) → best-effort "".
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if op := DetectInProgressOp(dir2); op != "" {
		t.Fatalf("linked worktree: want \"\", got %q", op)
	}
}
