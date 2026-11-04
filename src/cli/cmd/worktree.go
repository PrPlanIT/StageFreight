package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/cobra"
)

var (
	worktreeNewBranch string
	worktreeDetach    bool
	worktreeForce     bool
)

func init() {
	worktreeAddCmd.Flags().StringVarP(&worktreeNewBranch, "branch", "b", "", "create a new branch for the worktree")
	worktreeAddCmd.Flags().BoolVar(&worktreeDetach, "detach", false, "check out a detached HEAD")
	worktreeRemoveCmd.Flags().BoolVarP(&worktreeForce, "force", "f", false, "remove even with uncommitted changes or commits that exist nowhere else")
	worktreeCmd.AddCommand(worktreeAddCmd, worktreeListCmd, worktreeRemoveCmd, worktreePruneCmd)
	rootCmd.AddCommand(worktreeCmd)
}

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Manage worktrees, as git worktree does",
	Long: `One checkout per line of work, so parallel work never shares an index.

The commands, flags and defaults are git worktree's. StageFreight shows the plan before
adding, and list and remove also say what each worktree holds that exists nowhere else.`,
}

var worktreeAddCmd = &cobra.Command{
	Use:   "add [-b <new-branch> | --detach] <path> [<commit-ish>]",
	Short: "Create a worktree at <path>",
	Long: `Create a worktree at <path>, as git worktree add does:
  -b <new-branch>   a new branch at <commit-ish> (default HEAD)
  <path> alone      the branch named after the path: checked out if it exists, else created from HEAD
  <commit-ish>      a local branch is checked out; a branch exactly one remote has is
                    created tracking it; anything else is checked out detached
  --detach          a detached HEAD at <commit-ish> (default HEAD)`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		opts := gitstate.WorktreeAddOptions{Path: args[0], NewBranch: worktreeNewBranch, Detach: worktreeDetach}
		if len(args) == 2 {
			opts.CommitIsh = args[1]
		}
		return runWorktreeAdd(os.Stdout, dir, opts)
	},
}

func runWorktreeAdd(out io.Writer, dir string, opts gitstate.WorktreeAddOptions) error {
	start := time.Now()
	plan, err := gitstate.PlanWorktreeAdd(dir, opts)
	if err != nil {
		return err
	}
	sec := output.NewSection(out, "Worktree", time.Since(start), output.UseColor())
	sec.Row("%-12s%s", "path", plan.Path)
	switch {
	case plan.Branch == "":
		sec.Row("%-12s%s", "head", "detached")
	case plan.NewBranch && plan.Track != "":
		sec.Row("%-12s%s (new, tracking %s)", "branch", plan.Branch, plan.Track)
	case plan.NewBranch:
		sec.Row("%-12s%s (new)", "branch", plan.Branch)
	default:
		sec.Row("%-12s%s", "branch", plan.Branch)
	}
	sec.Row("%-12s%s", "commit", commitLine(dir, plan.Commit))
	if err := gitstate.ApplyWorktreeAdd(plan); err != nil {
		sec.Close()
		return fmt.Errorf("worktree add: %w", err)
	}
	sec.Row("%-12s%s", "status", "created")
	sec.Close()
	return nil
}

// commitLine renders a commit as its short hash and subject.
func commitLine(dir string, h plumbing.Hash) string {
	short := h.String()[:8]
	repo, err := gitstate.OpenRepo(dir)
	if err != nil {
		return short
	}
	c, err := repo.CommitObject(h)
	if err != nil {
		return short
	}
	subject, _, _ := strings.Cut(c.Message, "\n")
	return short + " " + subject
}

var worktreeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktrees and what each holds",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		return runWorktreeList(os.Stdout, dir)
	},
}

func runWorktreeList(out io.Writer, dir string) error {
	start := time.Now()
	wts, err := gitstate.Worktrees(dir)
	if err != nil {
		return err
	}
	def := defaultBranch(dir)
	sec := output.NewSection(out, "Worktrees", time.Since(start), output.UseColor())
	for _, w := range wts {
		where := w.Branch
		if where == "" {
			where = "(detached)"
		}
		var notes []string
		switch {
		case w.Prunable:
			notes = append(notes, "prunable: directory is gone")
		default:
			s, err := gitstate.InspectWorktree(w, def)
			if err != nil {
				notes = append(notes, "unreadable: "+err.Error())
				break
			}
			notes = append(notes, s.Head.String()[:8])
			if s.Upstream != "" {
				notes = append(notes, fmt.Sprintf("%s +%d -%d", s.Upstream, s.Ahead, s.Behind))
			} else if !s.Elsewhere {
				notes = append(notes, "commits on no other branch")
			}
			if s.Changes > 0 {
				notes = append(notes, fmt.Sprintf("%d changed", s.Changes))
			}
			if s.Merged {
				notes = append(notes, "merged into "+s.MergedInto)
			}
		}
		if w.Locked {
			notes = append(notes, "locked")
		}
		sec.Row("%s  %s", w.Path, where)
		sec.Row("    %s", strings.Join(notes, " · "))
	}
	sec.Close()
	return nil
}

// defaultBranch is the branch the remote's HEAD names (origin/HEAD), else main or master
// when the repository has one.
func defaultBranch(dir string) string {
	repo, err := gitstate.OpenRepo(dir)
	if err != nil {
		return ""
	}
	if ref, err := repo.Reference(plumbing.NewRemoteHEADReferenceName("origin"), false); err == nil && ref.Type() == plumbing.SymbolicReference {
		_, branch, _ := strings.Cut(ref.Target().Short(), "/")
		return branch
	}
	for _, b := range []string{"main", "master"} {
		if _, err := repo.Reference(plumbing.NewBranchReferenceName(b), false); err == nil {
			return b
		}
	}
	return ""
}

var worktreeRemoveCmd = &cobra.Command{
	Use:   "remove [--force] <worktree>",
	Short: "Remove a worktree, keeping its branch",
	Long: `Remove a worktree, as git worktree remove does: its directory and administrative files go,
its branch stays. Uncommitted changes refuse it, and so do commits that exist nowhere else
(ahead of its upstream, or on no other branch); --force removes it anyway.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		w, err := gitstate.RemoveWorktree(dir, args[0], worktreeForce)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "  removed %s (branch %s kept)\n", w.Path, orDetached(w.Branch))
		return nil
	},
}

func orDetached(branch string) string {
	if branch == "" {
		return "none: detached"
	}
	return branch
}

var worktreePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove administrative files of worktrees whose directory is gone",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		pruned, err := gitstate.PruneWorktrees(dir)
		if err != nil {
			return err
		}
		for _, id := range pruned {
			fmt.Fprintf(os.Stdout, "  pruned %s\n", id)
		}
		return nil
	},
}
