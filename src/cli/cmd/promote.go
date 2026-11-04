package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/workflow"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/spf13/cobra"
)

var (
	promoteTo     string
	promoteTitle  string
	promoteRemote string
	promoteYes    bool
)

func init() {
	promoteCmd.Flags().StringVar(&promoteTo, "to", "", "the branch to promote into, when the target class has more than one")
	promoteCmd.Flags().StringVar(&promoteTitle, "title", "", "merge request title (default: the latest commit's subject)")
	promoteCmd.Flags().StringVar(&promoteRemote, "remote", "origin", "git remote the branches live on")
	promoteCmd.Flags().BoolVar(&promoteYes, "yes", false, "approve a fast-forward of a shared branch without prompting")
	promoteCmd.Flags().StringVar(&workflowRole, "role", "", "act as this workflow role (audience, talent, director, producer); talent by default, at most your access allows")
	rootCmd.AddCommand(promoteCmd)
}

var promoteCmd = &cobra.Command{
	Use:   "promote [<promotion>]",
	Short: "Move the current branch's work along the declared workflow",
	Long: `Carry the current branch's work to the next class, as workflow.promote declares.

With no argument, the one promotion out of the current branch's class is used; name one when
several start there.
  merge_request   pushes the branch and opens a merge request into the target branch
  fast_forward    advances the target branch to your HEAD (a director's move for a long-lived branch)

The target branch is the default branch when it belongs to the target class; otherwise name
it with --to.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		id := ""
		if len(args) == 1 {
			id = args[0]
		}
		return runPromote(os.Stdout, dir, id)
	},
}

func runPromote(out io.Writer, dir, id string) error {
	repo, err := gitstate.OpenRepo(dir)
	if err != nil {
		return err
	}
	branch, ok := gitstate.CurrentBranch(repo)
	if !ok {
		return errors.New("promote: HEAD is detached; promote from a branch")
	}
	p, err := workflow.Promotion(cfg, branch, id)
	if err != nil {
		return fmt.Errorf("promote: %w", err)
	}
	target, err := promotionTarget(repo, p, promoteTo)
	if err != nil {
		return fmt.Errorf("promote: %w", err)
	}
	fmt.Fprintf(out, "  promote %s: %s → %s (%s → %s, by %s)\n", p.ID, branch, target, p.From, p.To, p.Method)

	switch p.Method {
	case config.PromoteMergeRequest:
		return promoteByMergeRequest(out, dir, repo, p, branch, target)
	case config.PromoteFastForward:
		return runPlanned(dir, promoteRemote, promoteYes, false, out, func(e *commit.Engine, pol gitplan.Policy) (gitplan.Plan, error) {
			return e.PlanTo(promoteRemote, target, pol)
		})
	}
	return fmt.Errorf("promote: %s promotions are declared but StageFreight doesn't carry them out yet; do this one by hand", p.Method)
}

// promotionTarget resolves the branch a promotion lands on: --to when given (it must belong
// to the target class), else the default branch when it belongs to the class, else the one
// branch on the remote that does.
func promotionTarget(repo *git.Repository, p config.Promotion, to string) (string, error) {
	inClass := func(b string) bool {
		c, ok := workflow.ClassOf(cfg, b)
		return ok && c.ID == p.To
	}
	if to != "" {
		if !inClass(to) {
			return "", fmt.Errorf("--to %s isn't a %s branch", to, p.To)
		}
		return to, nil
	}
	if primary, err := config.ResolvePrimary(cfg.Repos, cfg.Forges, cfg.Vars); err == nil && primary != nil && inClass(primary.DefaultBranch) {
		return primary.DefaultBranch, nil
	}
	var found []string
	refs, err := repo.References()
	if err != nil {
		return "", err
	}
	prefix := "refs/remotes/" + promoteRemote + "/"
	_ = refs.ForEach(func(ref *plumbing.Reference) error {
		if b, ok := strings.CutPrefix(ref.Name().String(), prefix); ok && b != "HEAD" && inClass(b) {
			found = append(found, b)
		}
		return nil
	})
	if len(found) == 1 {
		return found[0], nil
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no %s branch on %s to promote into; name one with --to", p.To, promoteRemote)
	}
	return "", fmt.Errorf("several %s branches (%s); name one with --to", p.To, strings.Join(found, ", "))
}

// promoteByMergeRequest pushes the branch (held to the workflow like any push) and opens a
// merge request from it into target, listing what it carries.
func promoteByMergeRequest(out io.Writer, dir string, repo *git.Repository, p config.Promotion, branch, target string) error {
	if err := refspecPush(dir, promoteRemote, "HEAD:refs/heads/"+branch, false, false); err != nil {
		return err
	}
	commits := commitsAhead(repo, plumbing.NewRemoteReferenceName(promoteRemote, target))
	title := promoteTitle
	if title == "" {
		if len(commits) > 0 {
			title = commits[0]
		} else {
			title = branch
		}
	}
	var desc strings.Builder
	fmt.Fprintf(&desc, "Promotes `%s` into `%s` (workflow.promote.%s: %s → %s).\n", branch, target, p.ID, p.From, p.To)
	if len(p.Require.Checks) > 0 {
		fmt.Fprintf(&desc, "\nRequires: %s.\n", strings.Join(p.Require.Checks, ", "))
	}
	if len(commits) > 0 {
		desc.WriteString("\n")
		for _, c := range commits {
			fmt.Fprintf(&desc, "- %s\n", c)
		}
	}

	primary, err := config.ResolvePrimary(cfg.Repos, cfg.Forges, cfg.Vars)
	if err != nil || primary == nil {
		return fmt.Errorf("promote: the branch is pushed, but no primary repo is declared to open the merge request on")
	}
	fc, err := forge.NewFromAccessory(primary.Provider, primary.BaseURL, primary.Project, primary.Credentials)
	if err != nil {
		return fmt.Errorf("promote: the branch is pushed, but the forge can't be reached to open the merge request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mr, err := fc.CreateMR(ctx, forge.MROptions{Title: title, Description: desc.String(), SourceBranch: branch, TargetBranch: target})
	if err != nil {
		return fmt.Errorf("promote: the branch is pushed, but opening the merge request failed: %w", err)
	}
	fmt.Fprintf(out, "  ✓ merge request !%s opened: %s\n", mr.ID, mr.URL)
	return nil
}

// commitsAhead returns the subjects of the commits on HEAD that target lacks, newest first.
func commitsAhead(repo *git.Repository, target plumbing.ReferenceName) []string {
	head, err := repo.Head()
	if err != nil {
		return nil
	}
	var base plumbing.Hash
	if ref, err := repo.Reference(target, true); err == nil {
		hc, err1 := repo.CommitObject(head.Hash())
		tc, err2 := repo.CommitObject(ref.Hash())
		if err1 == nil && err2 == nil {
			if bases, err := hc.MergeBase(tc); err == nil && len(bases) > 0 {
				base = bases[0].Hash
			}
		}
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil
	}
	var out []string
	_ = iter.ForEach(func(c *object.Commit) error {
		if c.Hash == base || len(out) == 50 {
			return storer.ErrStop
		}
		subject, _, _ := strings.Cut(c.Message, "\n")
		out = append(out, subject)
		return nil
	})
	return out
}
