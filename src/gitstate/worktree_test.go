package gitstate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// worktreeRepo builds a repository with one commit on main, a local branch "topic", and a
// remote "origin" with a branch only it has ("remote-only"). It returns the repository's
// directory and a runner for real git, which every assertion goes through: a worktree
// StageFreight creates must be one git accepts.
func worktreeRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("system git required for interop")
	}
	tmp := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	remote := filepath.Join(tmp, "remote.git")
	repo := filepath.Join(tmp, "repo")
	run(tmp, "init", "-q", "--bare", remote)
	run(tmp, "init", "-q", repo)
	write(t, filepath.Join(repo, "a"), "a\n")
	run(repo, "add", "a")
	run(repo, "commit", "-q", "-m", "base")
	run(repo, "branch", "topic")
	run(repo, "remote", "add", "origin", remote)
	run(repo, "push", "-q", "origin", "main", "main:remote-only")
	run(repo, "fetch", "-q", "origin")
	return repo, run
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func add(t *testing.T, repo string, opts WorktreeAddOptions) WorktreeAdd {
	t.Helper()
	p, err := PlanWorktreeAdd(repo, opts)
	if err != nil {
		t.Fatalf("plan %+v: %v", opts, err)
	}
	if err := ApplyWorktreeAdd(p); err != nil {
		t.Fatalf("apply %+v: %v", opts, err)
	}
	return p
}

// Each of git's add forms lands on the branch git would choose, in a worktree git accepts.
func TestWorktreeAdd_GitForms(t *testing.T) {
	repo, run := worktreeRepo(t)
	base := filepath.Dir(repo)
	mainHead := run(repo, "rev-parse", "HEAD")

	cases := []struct {
		name     string
		opts     WorktreeAddOptions
		branch   string // what git reports; "HEAD" when detached
		upstream string
	}{
		{"new branch", WorktreeAddOptions{Path: filepath.Join(base, "wt-b"), NewBranch: "feat"}, "feat", ""},
		{"path names an existing branch", WorktreeAddOptions{Path: filepath.Join(base, "topic")}, "topic", ""},
		{"path names a new branch", WorktreeAddOptions{Path: filepath.Join(base, "fresh")}, "fresh", ""},
		{"commit-ish a remote has", WorktreeAddOptions{Path: filepath.Join(base, "wt-r"), CommitIsh: "remote-only"}, "remote-only", "origin/remote-only"},
		{"commit-ish a commit", WorktreeAddOptions{Path: filepath.Join(base, "wt-d"), CommitIsh: mainHead}, "HEAD", ""},
		{"detach", WorktreeAddOptions{Path: filepath.Join(base, "wt-x"), Detach: true}, "HEAD", ""},
		{"new branch from a remote branch tracks it", WorktreeAddOptions{Path: filepath.Join(base, "wt-t"), NewBranch: "tracked", CommitIsh: "origin/remote-only"}, "tracked", "origin/remote-only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			add(t, repo, c.opts)
			if got := run(c.opts.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != c.branch {
				t.Errorf("git sees HEAD %q, want %q", got, c.branch)
			}
			if got := run(c.opts.Path, "rev-parse", "HEAD"); got != mainHead {
				t.Errorf("checked out %s, want %s", got, mainHead)
			}
			if st := run(c.opts.Path, "status", "--porcelain"); st != "" {
				t.Errorf("a fresh worktree must be clean to git, got:\n%s", st)
			}
			if c.upstream != "" {
				if got := run(c.opts.Path, "rev-parse", "--abbrev-ref", "@{u}"); got != c.upstream {
					t.Errorf("upstream %q, want %q", got, c.upstream)
				}
			}
			if list := run(repo, "worktree", "list"); !strings.Contains(list, c.opts.Path) {
				t.Errorf("git worktree list doesn't show %s:\n%s", c.opts.Path, list)
			}
		})
	}
}

// add refuses what git refuses, and creates nothing when it does.
func TestWorktreeAdd_Refusals(t *testing.T) {
	repo, _ := worktreeRepo(t)
	base := filepath.Dir(repo)
	full := filepath.Join(base, "full")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(full, "x"), "x")

	for name, c := range map[string]struct {
		opts WorktreeAddOptions
		want string
	}{
		"existing branch for -b": {WorktreeAddOptions{Path: filepath.Join(base, "w1"), NewBranch: "topic"}, "already exists"},
		"branch checked out":     {WorktreeAddOptions{Path: filepath.Join(base, "w2"), CommitIsh: "main"}, "already checked out"},
		"non-empty path":         {WorktreeAddOptions{Path: full, NewBranch: "z"}, "isn't empty"},
		"-b with --detach":       {WorktreeAddOptions{Path: filepath.Join(base, "w3"), NewBranch: "z", Detach: true}, "mutually exclusive"},
		"unknown commit-ish":     {WorktreeAddOptions{Path: filepath.Join(base, "w4"), CommitIsh: "nope"}, "invalid reference"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := PlanWorktreeAdd(repo, c.opts); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(base, "w1")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a refused add must create nothing")
	}
}

// list sees worktrees git created as well as its own, and what each holds.
func TestWorktrees_ListsGitsAndOurs(t *testing.T) {
	repo, run := worktreeRepo(t)
	base := filepath.Dir(repo)
	run(repo, "worktree", "add", "-q", "-b", "by-git", filepath.Join(base, "by-git"))
	add(t, repo, WorktreeAddOptions{Path: filepath.Join(base, "by-sf"), NewBranch: "by-sf"})

	wts, err := Worktrees(filepath.Join(base, "by-sf")) // from inside a linked worktree
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 3 || !wts[0].Main || wts[0].Branch != "main" || wts[1].Branch != "by-git" || wts[2].Branch != "by-sf" {
		t.Fatalf("got %+v", wts)
	}

	write(t, filepath.Join(base, "by-sf", "new"), "n")
	s, err := InspectWorktree(wts[2], "main")
	if err != nil {
		t.Fatal(err)
	}
	if s.Changes != 1 || !s.Elsewhere || !s.Merged {
		t.Errorf("state %+v: want 1 change, contained elsewhere, merged", s)
	}
}

// remove refuses to lose work, git included; --force removes it, keeping the branch, and
// git agrees it's gone.
func TestRemoveWorktree(t *testing.T) {
	repo, run := worktreeRepo(t)
	base := filepath.Dir(repo)
	wt := filepath.Join(base, "work")
	add(t, repo, WorktreeAddOptions{Path: wt, NewBranch: "work"})

	write(t, filepath.Join(wt, "dirty"), "d")
	var refusal *WorktreeRefusal
	if _, err := RemoveWorktree(repo, wt, false); !errors.As(err, &refusal) || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("dirty worktree: err = %v", err)
	}

	run(wt, "add", "dirty")
	run(wt, "commit", "-q", "-m", "only here")
	if _, err := RemoveWorktree(repo, wt, false); !errors.As(err, &refusal) || !strings.Contains(err.Error(), "no other branch has") {
		t.Fatalf("unique commit: err = %v", err)
	}

	if _, err := RemoveWorktree(repo, "work", true); err != nil { // by id, forced
		t.Fatalf("forced remove: %v", err)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Error("worktree directory remains")
	}
	if list := run(repo, "worktree", "list"); strings.Contains(list, wt) {
		t.Errorf("git still lists it:\n%s", list)
	}
	if run(repo, "rev-parse", "--verify", "-q", "work") == "" {
		t.Error("the branch must be kept")
	}

	if _, err := RemoveWorktree(repo, repo, true); err == nil || !strings.Contains(err.Error(), "main worktree") {
		t.Errorf("main worktree: err = %v", err)
	}
}

// A clean worktree whose commits its upstream has is removed without force, and git can
// remove one StageFreight made.
func TestRemoveWorktree_CleanAndGitInterop(t *testing.T) {
	repo, run := worktreeRepo(t)
	base := filepath.Dir(repo)
	tracked := filepath.Join(base, "tracked")
	add(t, repo, WorktreeAddOptions{Path: tracked, CommitIsh: "remote-only"})
	if _, err := RemoveWorktree(repo, tracked, false); err != nil {
		t.Fatalf("clean, pushed worktree: %v", err)
	}

	ours := filepath.Join(base, "ours")
	add(t, repo, WorktreeAddOptions{Path: ours, NewBranch: "ours"})
	run(repo, "worktree", "remove", ours)
	if _, err := os.Stat(ours); !errors.Is(err, os.ErrNotExist) {
		t.Error("git couldn't remove a worktree StageFreight made")
	}
}

// prune drops entries whose directory is gone and nothing else.
func TestPruneWorktrees(t *testing.T) {
	repo, run := worktreeRepo(t)
	base := filepath.Dir(repo)
	run(repo, "worktree", "add", "-q", "-b", "gone", filepath.Join(base, "gone"))
	add(t, repo, WorktreeAddOptions{Path: filepath.Join(base, "kept"), NewBranch: "kept"})
	if err := os.RemoveAll(filepath.Join(base, "gone")); err != nil {
		t.Fatal(err)
	}
	pruned, err := PruneWorktrees(repo)
	if err != nil || len(pruned) != 1 || pruned[0] != "gone" {
		t.Fatalf("pruned %v, %v", pruned, err)
	}
	list := run(repo, "worktree", "list")
	if strings.Contains(list, "gone") || !strings.Contains(list, "kept") {
		t.Errorf("git worktree list after prune:\n%s", list)
	}
}

// Two worktrees commit independently: no shared index.
func TestWorktree_IndependentIndex(t *testing.T) {
	repo, run := worktreeRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "side")
	add(t, repo, WorktreeAddOptions{Path: wt, NewBranch: "side"})

	write(t, filepath.Join(repo, "m"), "m")
	run(repo, "add", "m") // staged in main only
	write(t, filepath.Join(wt, "s"), "s")
	run(wt, "add", "s")
	run(wt, "commit", "-q", "-m", "side commit")

	if files := run(wt, "show", "--name-only", "--format=", "HEAD"); files != "s" {
		t.Errorf("side commit took %q; another worktree's staged file leaked in", files)
	}
	if st := run(repo, "status", "--porcelain"); st != "A  m" {
		t.Errorf("main's index changed: %q", st)
	}
}
