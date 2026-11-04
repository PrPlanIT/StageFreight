package gitstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// Worktree is one checkout of a repository: the main worktree or a linked one.
type Worktree struct {
	Path     string
	ID       string // linked worktrees: the name under $GIT_COMMON_DIR/worktrees
	Main     bool
	Branch   string // short name; empty when detached
	Head     plumbing.Hash
	Locked   bool
	Prunable bool // linked worktree whose directory is gone
	adminDir string
}

// layout locates a repository's common git dir and main worktree from any directory inside
// the main worktree or a linked one, as git does.
type layout struct {
	common string // $GIT_COMMON_DIR
	main   string // the main worktree's top directory
}

func repoLayout(dir string) (layout, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return layout{}, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		dotgit := filepath.Join(d, ".git")
		info, err := os.Stat(dotgit)
		if err == nil {
			if info.IsDir() {
				return layout{common: dotgit, main: d}, nil
			}
			gitDir, err := readGitFile(dotgit)
			if err != nil {
				return layout{}, err
			}
			common := gitDir
			if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
				common = resolveFrom(gitDir, strings.TrimSpace(string(b)))
			}
			return layout{common: common, main: filepath.Dir(common)}, nil
		}
		if filepath.Dir(d) == d {
			return layout{}, fmt.Errorf("%s is not inside a git repository", dir)
		}
	}
}

// readGitFile reads a `.git` file (`gitdir: <path>`) and returns the directory it names.
func readGitFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(b))
	target, ok := strings.CutPrefix(line, "gitdir: ")
	if !ok {
		return "", fmt.Errorf("%s is not a gitdir file", path)
	}
	return resolveFrom(filepath.Dir(path), target), nil
}

func resolveFrom(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}

// Worktrees lists the repository's worktrees, main first, then linked ones by id.
func Worktrees(dir string) ([]Worktree, error) {
	l, err := repoLayout(dir)
	if err != nil {
		return nil, err
	}
	main := Worktree{Path: l.main, Main: true, adminDir: l.common}
	readHead(&main)
	out := []Worktree{main}

	entries, err := os.ReadDir(filepath.Join(l.common, "worktrees"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var linked []Worktree
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		admin := filepath.Join(l.common, "worktrees", e.Name())
		w := Worktree{ID: e.Name(), adminDir: admin}
		if b, err := os.ReadFile(filepath.Join(admin, "gitdir")); err == nil {
			w.Path = filepath.Dir(resolveFrom(admin, strings.TrimSpace(string(b))))
		}
		if _, err := os.Stat(filepath.Join(w.Path, ".git")); w.Path == "" || err != nil {
			w.Prunable = true
		}
		if _, err := os.Stat(filepath.Join(admin, "locked")); err == nil {
			w.Locked = true
		}
		readHead(&w)
		linked = append(linked, w)
	}
	sort.Slice(linked, func(i, j int) bool { return linked[i].ID < linked[j].ID })
	return append(out, linked...), nil
}

// readHead fills a worktree's branch or detached commit from its HEAD file.
func readHead(w *Worktree) {
	b, err := os.ReadFile(filepath.Join(w.adminDir, "HEAD"))
	if err != nil {
		return
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		w.Branch = plumbing.ReferenceName(ref).Short()
		return
	}
	w.Head = plumbing.NewHash(head)
}

// WorktreeAdd is the resolved plan for `worktree add`, shown before it is applied.
type WorktreeAdd struct {
	Path      string
	Branch    string // empty when detached
	NewBranch bool
	Commit    plumbing.Hash
	Track     string // remote-tracking ref the new branch tracks, e.g. origin/main
	id        string
	common    string
	startName string
}

// WorktreeAddOptions are `worktree add`'s arguments, as git takes them.
type WorktreeAddOptions struct {
	Path      string
	NewBranch string // -b
	Detach    bool   // --detach
	CommitIsh string // optional
}

// PlanWorktreeAdd resolves `worktree add` exactly as git does, refusing what git refuses,
// without changing anything.
func PlanWorktreeAdd(dir string, opts WorktreeAddOptions) (WorktreeAdd, error) {
	if opts.Path == "" {
		return WorktreeAdd{}, errors.New("worktree add: a path is required")
	}
	if opts.NewBranch != "" && opts.Detach {
		return WorktreeAdd{}, errors.New("worktree add: -b and --detach are mutually exclusive")
	}
	l, err := repoLayout(dir)
	if err != nil {
		return WorktreeAdd{}, err
	}
	repo, err := OpenRepo(dir)
	if err != nil {
		return WorktreeAdd{}, err
	}
	path, err := filepath.Abs(opts.Path)
	if err != nil {
		return WorktreeAdd{}, err
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) > 0 {
		return WorktreeAdd{}, fmt.Errorf("worktree add: %s already exists and isn't empty", opts.Path)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return WorktreeAdd{}, fmt.Errorf("worktree add: %s already exists and isn't a directory", opts.Path)
	}

	p := WorktreeAdd{Path: path, common: l.common}
	branchExists := func(b string) bool {
		_, err := repo.Reference(plumbing.NewBranchReferenceName(b), false)
		return err == nil
	}
	start := opts.CommitIsh
	if start == "" {
		start = "HEAD"
	}

	switch {
	case opts.NewBranch != "":
		if branchExists(opts.NewBranch) {
			return WorktreeAdd{}, fmt.Errorf("worktree add: a branch named %q already exists", opts.NewBranch)
		}
		p.Branch, p.NewBranch = opts.NewBranch, true
	case opts.Detach:
	case opts.CommitIsh == "":
		// git names the branch after the path: check it out if it exists, else create it.
		p.Branch = filepath.Base(path)
		p.NewBranch = !branchExists(p.Branch)
	case branchExists(opts.CommitIsh):
		p.Branch = opts.CommitIsh
	default:
		// A name exactly one remote has as a branch becomes a local branch tracking it.
		if remote, ok := uniqueRemoteBranch(repo, opts.CommitIsh); ok {
			p.Branch, p.NewBranch = opts.CommitIsh, true
			start = remote + "/" + opts.CommitIsh
		}
	}

	if p.Branch != "" && !p.NewBranch {
		start = p.Branch
		if wts, err := Worktrees(dir); err == nil {
			for _, w := range wts {
				if w.Branch == p.Branch && !w.Prunable {
					return WorktreeAdd{}, fmt.Errorf("worktree add: %q is already checked out at %s", p.Branch, w.Path)
				}
			}
		}
	}
	h, err := repo.ResolveRevision(plumbing.Revision(start))
	if err != nil {
		return WorktreeAdd{}, fmt.Errorf("worktree add: invalid reference: %s", start)
	}
	p.Commit, p.startName = *h, start
	if p.NewBranch {
		if _, err := repo.Reference(plumbing.NewRemoteReferenceName(splitRemote(start)), false); err == nil {
			p.Track = start
		}
	}
	p.id = uniqueID(l.common, filepath.Base(path))
	return p, nil
}

// splitRemote splits "origin/main" into "origin", "main"; anything else yields empty parts.
func splitRemote(name string) (string, string) {
	remote, branch, ok := strings.Cut(name, "/")
	if !ok {
		return "", ""
	}
	return remote, branch
}

// uniqueRemoteBranch returns the one remote that has branch, as git's add DWIM requires.
func uniqueRemoteBranch(repo *git.Repository, branch string) (string, bool) {
	remotes, err := repo.Remotes()
	if err != nil {
		return "", false
	}
	var found []string
	for _, r := range remotes {
		name := r.Config().Name
		if _, err := repo.Reference(plumbing.NewRemoteReferenceName(name, branch), false); err == nil {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// uniqueID returns git's admin-directory name for a worktree: the path's last element, with
// a number appended when it is taken.
func uniqueID(common, base string) string {
	id := base
	for n := 1; ; n++ {
		if _, err := os.Stat(filepath.Join(common, "worktrees", id)); errors.Is(err, os.ErrNotExist) {
			return id
		}
		id = fmt.Sprintf("%s%d", base, n)
	}
}

// ApplyWorktreeAdd creates the worktree a plan describes: git's linked-worktree layout,
// the branch (with its upstream when it tracks a remote branch), and a checkout of the
// commit. On failure it removes what it created.
func ApplyWorktreeAdd(p WorktreeAdd) (err error) {
	admin := filepath.Join(p.common, "worktrees", p.id)
	createdDir := false
	if _, statErr := os.Stat(p.Path); errors.Is(statErr, os.ErrNotExist) {
		createdDir = true
	}
	defer func() {
		if err != nil {
			os.RemoveAll(admin)
			if createdDir {
				os.RemoveAll(p.Path)
			} else {
				os.Remove(filepath.Join(p.Path, ".git"))
			}
		}
	}()
	if err := os.MkdirAll(admin, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Path, 0o755); err != nil {
		return err
	}

	repo, err := git.PlainOpen(filepath.Dir(p.common))
	if err != nil {
		return err
	}
	head := p.Commit.String()
	if p.Branch != "" {
		ref := plumbing.NewBranchReferenceName(p.Branch)
		head = "ref: " + ref.String()
		if p.NewBranch {
			if err := repo.Storer.SetReference(plumbing.NewHashReference(ref, p.Commit)); err != nil {
				return err
			}
			if p.Track != "" {
				if err := setUpstream(repo, p.Branch, p.Track); err != nil {
					return err
				}
			}
		}
	}
	files := map[string]string{
		filepath.Join(admin, "HEAD"):      head + "\n",
		filepath.Join(admin, "commondir"): "../..\n",
		filepath.Join(admin, "gitdir"):    filepath.Join(p.Path, ".git") + "\n",
		filepath.Join(p.Path, ".git"):     "gitdir: " + admin + "\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}

	wr, err := OpenRepo(p.Path)
	if err != nil {
		return err
	}
	wt, err := wr.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&git.ResetOptions{Commit: p.Commit, Mode: git.HardReset})
}

// setUpstream records that branch tracks a remote-tracking ref such as origin/main.
func setUpstream(repo *git.Repository, branch, track string) error {
	remote, remoteBranch := splitRemote(track)
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	cfg.Branches[branch] = &gitconfig.Branch{
		Name:   branch,
		Remote: remote,
		Merge:  plumbing.NewBranchReferenceName(remoteBranch),
	}
	return repo.Storer.SetConfig(cfg)
}

// PruneWorktrees removes the administrative files of linked worktrees whose directory is
// gone, leaving locked ones, as `git worktree prune` does. It returns the ids it pruned.
func PruneWorktrees(dir string) ([]string, error) {
	wts, err := Worktrees(dir)
	if err != nil {
		return nil, err
	}
	var pruned []string
	for _, w := range wts {
		if w.Main || !w.Prunable || w.Locked {
			continue
		}
		if err := os.RemoveAll(w.adminDir); err != nil {
			return pruned, err
		}
		pruned = append(pruned, w.ID)
	}
	return pruned, nil
}
