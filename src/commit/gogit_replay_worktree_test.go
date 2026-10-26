package commit

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// A replay rewrites only what the rebase changes. Files git does not track —
// untracked or ignored — are the operator's, and `git rebase --autostash` never
// touches them; the replay must not either.

// divergedWithIgnore builds a repo whose .gitignore (build/) is on both sides, then
// diverges: upstream adds upstream.txt, local commits local.txt.
func divergedWithIgnore(t *testing.T) *replayTestRepo {
	t.Helper()
	r := newReplayTestRepo(t)
	r.addSFCommit(t, ".gitignore", "build/\n", "chore: ignore build")
	if err := r.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{"refs/heads/main:refs/heads/main"},
	}); err != nil {
		t.Fatalf("push .gitignore: %v", err)
	}
	r.advanceRemote(t, "upstream.txt", "from remote")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	return r
}

// operatorFiles are files git does not track; every one must survive the replay.
var operatorFiles = map[string]string{
	"notes.txt":             "untracked at the root",
	"scratch/deep/draft.md": "untracked in an untracked directory",
	"build/out.bin":         "ignored output",
	".env":                  "untracked dotfile",
}

func TestReplay_PreservesUntrackedAndIgnored(t *testing.T) {
	r := divergedWithIgnore(t)
	for name, content := range operatorFiles {
		writeTestFile(t, r.localDir, name, content)
	}

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}

	for name, want := range operatorFiles {
		got, err := os.ReadFile(filepath.Join(r.localDir, name))
		if err != nil || string(got) != want {
			t.Errorf("replay destroyed operator file %s: got %q (%v)", name, got, err)
		}
	}
	for name, want := range map[string]string{"upstream.txt": "from remote", "local.txt": "local content"} {
		if got, err := os.ReadFile(filepath.Join(r.localDir, name)); err != nil || string(got) != want {
			t.Errorf("rebased tree missing %s: got %q (%v)", name, got, err)
		}
	}
}

// Upstream adding a path the operator has as an untracked file must refuse before any
// mutation, as git does ("untracked working tree files would be overwritten").
func TestReplay_RefusesToOverwriteUntracked(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "notes.txt", "upstream's notes")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "notes.txt", "my private notes")
	before := headHash(t, r.localDir)

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := Replay(session); err == nil {
		t.Fatal("replay must refuse when upstream would overwrite an untracked file")
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "notes.txt")); string(got) != "my private notes" {
		t.Errorf("untracked notes.txt overwritten: %q", got)
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("refusal must not move the branch: %s -> %s", before, after)
	}
}

// A replay that fails partway leaves the branch and the tracked files where they were.
func TestReplay_FailureRestoresBranch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection needs a non-root user")
	}
	r := newReplayTestRepo(t)
	r.addSFCommit(t, "ro/f.txt", "v1", "chore: add ro/f.txt")
	if err := r.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{"refs/heads/main:refs/heads/main"},
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
	r.advanceRemote(t, "ro/f.txt", "v2 from remote")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	before := headHash(t, r.localDir)

	// The rebase must rewrite ro/f.txt; make that write fail.
	ro := filepath.Join(r.localDir, "ro")
	if err := os.Chmod(filepath.Join(ro, "f.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := Replay(session); err == nil {
		t.Fatal("replay must fail when it cannot write a tracked file")
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("failed replay left the branch moved: %s -> %s", before, after)
	}
	if got, _ := os.ReadFile(filepath.Join(ro, "f.txt")); string(got) != "v1" {
		t.Errorf("failed replay changed ro/f.txt: %q", got)
	}
}

// Parity with git: the same fixture through `git pull --rebase --autostash` and through
// Replay must leave byte-identical working trees.
func TestReplay_WorktreeMatchesGitRebaseAutostash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required for the parity check")
	}
	r := divergedWithIgnore(t)
	for name, content := range operatorFiles {
		writeTestFile(t, r.localDir, name, content)
	}
	writeTestFile(t, r.localDir, "README.md", "uncommitted tracked edit")

	twin := filepath.Join(t.TempDir(), "twin")
	if out, err := exec.Command("cp", "-a", r.localDir, twin).CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v\n%s", err, out)
	}
	gitCmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "pull", "--rebase", "--autostash", "-q", "origin", "main")
	gitCmd.Dir = twin
	if out, err := gitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git pull --rebase --autostash: %v\n%s", err, out)
	}

	session := openSession(t, r.localDir)
	if err := session.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}

	compareWorktrees(t, snapshotWorktree(t, twin), snapshotWorktree(t, r.localDir))
}

// snapshotWorktree maps every file outside .git to its content.
func snapshotWorktree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = "symlink -> " + target
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fmt.Sprintf("%s [exec=%t]", b, fi.Mode()&0o100 != 0)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// advanceRemoteWith commits whatever edit makes to a clone of the remote and pushes it.
func (r *replayTestRepo) advanceRemoteWith(t *testing.T, edit func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	clone, err := git.PlainClone(dir, false, &git.CloneOptions{URL: r.remoteDir})
	if err != nil {
		t.Fatalf("clone remote: %v", err)
	}
	wt, _ := clone.Worktree()
	edit(dir)
	testCommit(t, wt, "chore: remote advance")
	if err := clone.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("push remote advance: %v", err)
	}
}

// gitIn runs the git CLI in dir; fixtures use it where go-git cannot stage a change
// (a file and a directory swapping places).
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *replayTestRepo) pushMain(t *testing.T) {
	t.Helper()
	if err := r.repo.Push(&git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{"refs/heads/main:refs/heads/main"},
	}); err != nil {
		t.Fatalf("push: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Modes, symlinks, a file that becomes a directory and an ignored file upstream starts
// tracking all land exactly as `git pull --rebase --autostash` lands them.
func TestReplay_WorktreeMatchesGit_ModesSymlinksSwaps(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required for the parity check")
	}
	r := newReplayTestRepo(t)
	r.addSFCommit(t, ".gitignore", "build/\n", "chore: ignore build")
	r.addSFCommit(t, "scripts/build.sh", "#!/bin/sh\n", "chore: script")
	r.addSFCommit(t, "a", "a was a file\n", "chore: a")
	r.pushMain(t)
	r.advanceRemoteWith(t, func(dir string) {
		must(t, os.Chmod(filepath.Join(dir, "scripts/build.sh"), 0o755))
		must(t, os.MkdirAll(filepath.Join(dir, "docs/new"), 0o755))
		must(t, os.Symlink("../index.md", filepath.Join(dir, "docs/new/link")))
		must(t, os.Remove(filepath.Join(dir, "a")))
		writeTestFile(t, dir, "a/b.txt", "a is a directory now\n")
		writeTestFile(t, dir, "build/tracked.txt", "upstream tracks this\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "add", "-f", "build/tracked.txt")
	})
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "build/tracked.txt", "my ignored build output")
	writeTestFile(t, r.localDir, "notes.txt", "untracked notes")

	twin := filepath.Join(t.TempDir(), "twin")
	if out, err := exec.Command("cp", "-a", r.localDir, twin).CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v\n%s", err, out)
	}
	gitCmd := exec.Command("git", "-c", "user.name=t", "-c", "user.email=t@t", "pull", "--rebase", "--autostash", "-q", "origin", "main")
	gitCmd.Dir = twin
	if out, err := gitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git pull --rebase --autostash: %v\n%s", err, out)
	}

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}
	compareWorktrees(t, snapshotWorktree(t, twin), snapshotWorktree(t, r.localDir))
}

// A checkout that fails partway — x/a already deleted, y/new.txt cannot be written —
// restores x/a and the branch.
func TestReplay_RollbackRestoresPartialCheckout(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based failure injection needs a non-root user")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required to stage the fixture")
	}
	r := newReplayTestRepo(t)
	r.addSFCommit(t, "x/a", "x/a was here\n", "chore: x/a")
	r.addSFCommit(t, "y/keep", "keep", "chore: y")
	r.pushMain(t)
	r.advanceRemoteWith(t, func(dir string) {
		must(t, os.Remove(filepath.Join(dir, "x/a")))
		writeTestFile(t, dir, "y/new.txt", "new\n")
		gitIn(t, dir, "add", "-A")
	})
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	before := headHash(t, r.localDir)
	y := filepath.Join(r.localDir, "y")
	must(t, os.Chmod(y, 0o555))
	t.Cleanup(func() { _ = os.Chmod(y, 0o755) })

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err == nil {
		t.Fatal("replay must fail when it cannot write y/new.txt")
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("failed replay left the branch moved: %s -> %s", before, after)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "x/a")); string(got) != "x/a was here\n" {
		t.Errorf("rollback must restore x/a, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(y, "new.txt")); err == nil {
		t.Error("rollback left y/new.txt behind")
	}
}

// A path the local commits add and later delete is absent at HEAD and at the replayed
// head, so the replay never writes it and the operator's untracked file there stays.
func TestReplay_LeavesUntrackedOnIntermediatePath(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "upstream.txt", "from remote")
	r.addSFCommit(t, "tmp/gen.txt", "generated", "feat: add gen")
	wt, _ := r.repo.Worktree()
	_, err := wt.Remove("tmp/gen.txt")
	must(t, err)
	testCommit(t, wt, "feat: drop gen\n\n"+sfGeneratedTrailer)
	writeTestFile(t, r.localDir, "tmp/gen.txt", "operator's own file")

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "tmp/gen.txt")); string(got) != "operator's own file" {
		t.Errorf("untracked tmp/gen.txt changed: %q", got)
	}
}

// Upstream replacing a tracked directory with a file must refuse while the directory
// still holds an untracked file, before touching anything.
func TestReplay_RefusesDirToFileOverUntracked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required to stage the fixture")
	}
	r := newReplayTestRepo(t)
	r.addSFCommit(t, "a/b.txt", "tracked", "chore: a/b.txt")
	r.pushMain(t)
	r.advanceRemoteWith(t, func(dir string) {
		must(t, os.RemoveAll(filepath.Join(dir, "a")))
		writeTestFile(t, dir, "a", "a is a file now\n")
		gitIn(t, dir, "add", "-A")
	})
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "a/notes.txt", "operator notes")
	before := headHash(t, r.localDir)

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err == nil || !strings.Contains(err.Error(), "a/notes.txt") {
		t.Fatalf("replay must refuse up front, naming the untracked a/notes.txt; got %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "a/notes.txt")); string(got) != "operator notes" {
		t.Errorf("untracked a/notes.txt changed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "a/b.txt")); string(got) != "tracked" {
		t.Errorf("tracked a/b.txt changed before the refusal: %q", got)
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("refusal must not move the branch: %s -> %s", before, after)
	}
}

func compareWorktrees(t *testing.T, want, got map[string]string) {
	t.Helper()
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s: git has %q, replay has %q", p, c, got[p])
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s: present after replay, absent after git", p)
		}
	}
}

// A local commit deletes p and the operator has since made a new, untracked p: p is
// absent at HEAD and at the replayed head, so the replay never writes it.
func TestReplay_LeavesUntrackedOnLocallyDeletedPath(t *testing.T) {
	r := newReplayTestRepo(t)
	r.addSFCommit(t, "p.txt", "tracked once", "chore: p")
	r.pushMain(t)
	r.advanceRemote(t, "upstream.txt", "from remote")
	wt, _ := r.repo.Worktree()
	_, err := wt.Remove("p.txt")
	must(t, err)
	testCommit(t, wt, "feat: drop p\n\n"+sfGeneratedTrailer)
	writeTestFile(t, r.localDir, "p.txt", "operator's new p")

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "p.txt")); string(got) != "operator's new p" {
		t.Errorf("untracked p.txt changed: %q", got)
	}
}

// A directory upstream replaces with a file still holding an ignored file is refused up
// front (go-git cannot clear it), not failed partway with "is a directory".
func TestReplay_RefusesDirToFileOverIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required to stage the fixture")
	}
	r := newReplayTestRepo(t)
	r.addSFCommit(t, ".gitignore", "*.o\n", "chore: ignore objects")
	r.addSFCommit(t, "out/keep.txt", "tracked", "chore: out")
	r.pushMain(t)
	r.advanceRemoteWith(t, func(dir string) {
		must(t, os.RemoveAll(filepath.Join(dir, "out")))
		writeTestFile(t, dir, "out", "out is a file now\n")
		gitIn(t, dir, "add", "-A")
	})
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "out/build.o", "ignored object")
	before := headHash(t, r.localDir)

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err == nil || !strings.Contains(err.Error(), "out/build.o") {
		t.Fatalf("replay must refuse up front, naming out/build.o; got %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "out/build.o")); string(got) != "ignored object" {
		t.Errorf("ignored out/build.o changed: %q", got)
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("refusal must not move the branch: %s -> %s", before, after)
	}
}

// Upstream adds a/new while the local commit turns directory a into a file: git cannot
// hold both, so the replay refuses before writing a commit or touching anything.
func TestReplay_RefusesFileDirClash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required to stage the fixture")
	}
	r := newReplayTestRepo(t)
	r.addSFCommit(t, "a/b", "b", "chore: a/b")
	r.pushMain(t)
	r.advanceRemote(t, "a/new", "upstream adds into a/")
	must(t, os.RemoveAll(filepath.Join(r.localDir, "a")))
	writeTestFile(t, r.localDir, "a", "a is a file locally\n")
	gitIn(t, r.localDir, "add", "-A")
	gitIn(t, r.localDir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "feat: a becomes a file\n\n"+sfGeneratedTrailer)
	before := headHash(t, r.localDir)

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err == nil || !strings.Contains(err.Error(), "both a file and a directory") {
		t.Fatalf("replay must refuse the file/directory clash; got %v", err)
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("refusal must not move the branch: %s -> %s", before, after)
	}
}

// An ignored file sitting where upstream needs a directory cannot be cleared by the
// checkout, so the replay refuses up front, naming it.
func TestReplay_RefusesIgnoredFileWhereDirNeeded(t *testing.T) {
	r := newReplayTestRepo(t)
	r.addSFCommit(t, ".gitignore", "/out\n", "chore: ignore out")
	r.pushMain(t)
	r.advanceRemote(t, "out/x", "upstream adds out/x")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "out", "ignored file")
	before := headHash(t, r.localDir)

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err == nil || !strings.Contains(err.Error(), "would overwrite or remove out, which") {
		t.Fatalf("replay must refuse up front, naming out; got %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(r.localDir, "out")); string(got) != "ignored file" {
		t.Errorf("ignored out changed: %q", got)
	}
	if after := headHash(t, r.localDir); after != before {
		t.Errorf("refusal must not move the branch: %s -> %s", before, after)
	}
}

// Staged work on paths the replay does not write stays staged: a new file stays added
// and a staged edit stays in the index.
func TestReplay_PreservesStagedState(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "upstream.txt", "from remote")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	writeTestFile(t, r.localDir, "staged.txt", "staged new file")
	writeTestFile(t, r.localDir, "README.md", "staged readme edit")
	wt, _ := r.repo.Worktree()
	_, err := wt.Add("staged.txt")
	must(t, err)
	_, err = wt.Add("README.md")
	must(t, err)

	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))
	if err := Replay(session); err != nil {
		t.Fatalf("replay: %v", err)
	}
	st, err := wt.Status()
	must(t, err)
	if s := st.File("staged.txt"); s.Staging != git.Added {
		t.Errorf("staged.txt must stay added, staging=%q", s.Staging)
	}
	if s := st.File("README.md"); s.Staging != git.Modified {
		t.Errorf("README.md edit must stay staged, staging=%q", s.Staging)
	}
}

// A commit landing on the branch after the replay read it would be orphaned by moving
// the branch to a head built on the old one, so the replay aborts and changes nothing.
func TestReplay_RefusesWhenBranchMoved(t *testing.T) {
	r := newReplayTestRepo(t)
	r.advanceRemote(t, "upstream.txt", "from remote")
	r.addSFCommit(t, "local.txt", "local content", "feat: local change")
	session := openSession(t, r.localDir)
	must(t, session.Fetch("origin"))

	moved := r.addSFCommit(t, "later.txt", "committed meanwhile", "feat: meanwhile")
	if err := Replay(session); err == nil || !strings.Contains(err.Error(), "branch moved") {
		t.Fatalf("replay must abort when the branch moved; got %v", err)
	}
	if got := headHash(t, r.localDir); got != moved {
		t.Errorf("the meanwhile commit must stay the branch head: got %s, want %s", got, moved)
	}
}

// The same file changed on both sides is a rebase conflict, reported as one (not as
// corruption) before anything is written.
func TestReplay_ReportsUpstreamOverlapAsConflict(t *testing.T) {
	for name, upstream := range map[string]string{"different change": "upstream edit", "same change": "local edit"} {
		t.Run(name, func(t *testing.T) {
			r := newReplayTestRepo(t)
			r.advanceRemote(t, "README.md", upstream)
			r.addSFCommit(t, "README.md", "local edit", "feat: edit readme")
			before := headHash(t, r.localDir)

			session := openSession(t, r.localDir)
			must(t, session.Fetch("origin"))
			err := Replay(session)
			if err == nil || !strings.Contains(err.Error(), "upstream also changed README.md") {
				t.Fatalf("want a conflict naming README.md, got %v", err)
			}
			if strings.Contains(err.Error(), "corruption") {
				t.Errorf("a conflict must not read as corruption: %v", err)
			}
			if after := headHash(t, r.localDir); after != before {
				t.Errorf("refusal must not move the branch: %s -> %s", before, after)
			}
		})
	}
}

// A submodule pointer the replay moves is a gitlink: its checked-out files are the
// submodule's, never collisions.
func TestOperatorCollisions_SkipsSubmodules(t *testing.T) {
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	must(t, err)
	writeTestFile(t, root, "vendor/lib/.git", "gitdir: ../../.git/modules/lib\n")
	writeTestFile(t, root, "vendor/lib/src/main.c", "int main;\n")
	head := map[string]object.TreeEntry{"vendor/lib": {Name: "vendor/lib", Mode: filemode.Submodule}}

	got, err := operatorCollisions(repo, root, head, map[string]bool{"vendor/lib": true})
	must(t, err)
	if len(got) != 0 {
		t.Errorf("a tracked submodule must not collide, got %v", got)
	}
	got, err = operatorCollisions(repo, root, map[string]object.TreeEntry{}, map[string]bool{"vendor/lib": true})
	must(t, err)
	if len(got) != 0 {
		t.Errorf("a submodule the target adds must not collide, got %v", got)
	}
}

// Files git ignores through its default global ignore ($XDG_CONFIG_HOME/git/ignore)
// are expendable, exactly as in git.
func TestIgnoreMatcher_XDGGlobalIgnore(t *testing.T) {
	home, xdg, root := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	writeTestFile(t, xdg, "git/ignore", "*.log\n")
	repo, err := git.PlainInit(root, false)
	must(t, err)
	m, err := ignoreMatcher(repo, root)
	must(t, err)
	if !m.Match([]string{"build.log"}, false) {
		t.Error("build.log is ignored by the XDG global ignore and must match")
	}
	if m.Match([]string{"notes.txt"}, false) {
		t.Error("notes.txt is not ignored")
	}
}
