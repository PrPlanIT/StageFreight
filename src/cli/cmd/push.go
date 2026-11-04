package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/workflow"
	"github.com/spf13/cobra"
)

var (
	pushRemote   string
	pushRefspec  string
	pushNoRebase bool
	pushYes      bool
	pushOverride bool
)

func init() {
	pushCmd.Flags().StringVar(&pushRemote, "remote", "origin", "git remote to push to")
	pushCmd.Flags().StringVar(&pushRefspec, "refspec", "", "push HEAD to an explicit ref (e.g. HEAD:refs/heads/main), replaying onto the branch if it moved")
	pushCmd.Flags().BoolVar(&pushNoRebase, "no-rebase", false, "with --refspec: fail instead of replaying onto a branch that moved")
	pushCmd.Flags().BoolVar(&pushYes, "yes", false, "approve a transformational plan (e.g. replay onto a protected branch) without prompting")
	pushCmd.Flags().BoolVar(&pushOverride, "maintainer-override", false, "producer only: push even though the declared workflow refuses it (reported; the forge still enforces its own rules)")
	pushCmd.Flags().StringVar(&workflowRole, "role", "", "act as this workflow role (audience, talent, director, producer); talent by default, at most your access allows")
	rootCmd.AddCommand(pushCmd)
}

var pushCmd = &cobra.Command{
	Use:   "push [remote] [branch]",
	Short: "Plan and push the current branch to its remote (or an explicit destination)",
	Long: `Push the current branch to its remote, or to an explicit destination.

With no arguments, StageFreight plans against the branch's own upstream — uploading,
creating tracking, fast-forwarding, or (for a protected destination) replaying with your
confirmation. It never silently rewrites a feature branch.

With "push <remote> <branch>" it plans an explicit cross-target push (e.g. a feature branch
onto origin/main): ahead/behind are computed against that destination — fast-forward when it
is behind you (Confirm on protected trunk), refuse when it already contains you, and hand the
rebase back to git when the two have diverged.`,
	Args: cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		// Explicit --refspec (CI detached-HEAD): a direct push HEAD→ref, handled by the
		// planner's DirectPush inside GitBackend.Push (no reconcile).
		if pushRefspec != "" {
			return refspecPush(rootDir, pushRemote, pushRefspec, !pushNoRebase, pushOverride)
		}
		// push <remote> <branch>: explicit cross-target push, ahead/behind vs the destination.
		if len(args) == 2 {
			remote, branch := args[0], args[1]
			return runPlanned(rootDir, remote, pushYes, pushOverride, os.Stdout, func(e *commit.Engine, p gitplan.Policy) (gitplan.Plan, error) {
				return e.PlanTo(remote, branch, p)
			})
		}
		// push [remote]: own-upstream, with an optional remote override.
		remote := pushRemote
		if len(args) == 1 {
			remote = args[0]
		}
		return runPlannerPush(rootDir, remote, pushYes, pushOverride, os.Stdout)
	},
}

// runPlannerPush drives the planner flow: fetch → Plan → Render (always shown) → Execute
// (gated on the plan's interaction ops). Extracted from the command so it is testable on a
// scratch repo without cobra/chdir.
func runPlannerPush(rootDir, remote string, approved, override bool, out io.Writer) error {
	return runPlanned(rootDir, remote, approved, override, out, func(e *commit.Engine, p gitplan.Policy) (gitplan.Plan, error) {
		return e.Plan(p), nil
	})
}

// runPlanned is the shared fetch → Plan → Render (always) → Execute (gated) flow behind
// `push`, `push <remote> <branch>`, and `pull`; planFn selects the direction. A plan that
// sends commits is held to the declared workflow first; override pushes past a refusal.
func runPlanned(rootDir, remote string, approved, override bool, out io.Writer, planFn func(*commit.Engine, gitplan.Policy) (gitplan.Plan, error)) error {
	session, err := gitstate.OpenSyncSession(rootDir, cfg)
	if err != nil {
		return fmt.Errorf("opening repository: %w", err)
	}
	if err := session.Fetch(remote); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}
	eng := commit.NewEngine(session, commit.EngineOptions{
		OnEvent: func(ev gitstate.TransitionEvent) {
			if ev.Note != "" {
				fmt.Fprintf(out, "  %s\n", ev.Note)
			}
		},
	})
	plan, err := planFn(eng, gitplan.DefaultPolicy())
	if err != nil {
		return err
	}
	fmt.Fprint(out, gitplan.Render(plan))
	if sendsCommits(plan) {
		if err := pushGate(out, override, plan.Dest.Branch); err != nil {
			return err
		}
	}

	_, execErr := eng.Execute(plan, commit.ExecuteOptions{Approved: approved})
	var confirm *commit.ConfirmRequiredError
	var decide *commit.DecisionRequiredError
	var refuse *commit.RefusedError
	switch {
	case execErr == nil:
		fmt.Fprintln(out, "  ✓ done")
		return nil
	case errors.As(execErr, &confirm):
		fmt.Fprintf(out, "  needs confirmation: %s\n  re-run with --yes to proceed.\n", confirm.Detail)
		return silentExit(fmt.Errorf("confirmation required"))
	case errors.As(execErr, &decide):
		fmt.Fprintf(out, "  decision required: %s\n  choose one and re-run: %v\n", decide.Detail, decide.Choices)
		return silentExit(fmt.Errorf("decision required"))
	case errors.As(execErr, &refuse):
		fmt.Fprintf(out, "  %s\n", refuse.Detail)
		return silentExit(fmt.Errorf("refused"))
	default:
		return execErr
	}
}

// refspecPush handles an explicit --refspec push (CI detached-HEAD): GitBackend.Push →
// the planner's DirectPush (HEAD → ref, no reconcile), replaying onto the branch when it
// moved and replay is on. A thin entry point because the refspec case renders its own
// concise status.
func refspecPush(rootDir, remote, refspec string, replay, override bool) error {
	if _, dst, ok := strings.Cut(refspec, ":"); ok && !strings.HasPrefix(dst, "refs/tags/") {
		if err := pushGate(os.Stdout, override, strings.TrimPrefix(dst, "refs/heads/")); err != nil {
			return err
		}
	}
	opts := commit.PushOptions{
		Enabled:         true,
		Remote:          remote,
		Refspec:         refspec,
		RebaseOnDiverge: replay,
	}
	backend := &commit.GitBackend{RootDir: rootDir, Cfg: cfg}
	result, err := backend.Push(opts)
	if err != nil {
		return err
	}
	useColor := output.UseColor()
	sec := output.NewSection(os.Stdout, "Push", 0, useColor)
	if result.Noop {
		sec.Row("%-16s%s", "status", "already up to date")
	} else {
		output.RowStatus(sec, "pushed", opts.Remote, "success", useColor)
		for _, action := range result.ActionsExecuted {
			switch action {
			case commit.SyncRebase:
				sec.Row("%-16s%s", "sync", "rebased onto upstream before push")
			case commit.SyncFastForward:
				sec.Row("%-16s%s", "sync", "fast-forwarded to upstream")
			case commit.SyncSetUpstream:
				sec.Row("%-16s%s", "sync", "tracking branch configured")
			}
		}
	}
	sec.Close()
	return nil
}

// sendsCommits reports whether a plan pushes commits to its destination.
func sendsCommits(p gitplan.Plan) bool {
	for _, op := range p.Operations {
		switch op.Kind {
		case gitplan.OpUpload, gitplan.OpCreateTracking, gitplan.OpDirectPush:
			return true
		}
	}
	return false
}

// pushGate holds a push to branch to the declared workflow: how the branch takes changes,
// and whether your role may push to it.
func pushGate(out io.Writer, override bool, branch string) error {
	if !localWorkflow() {
		return nil
	}
	s, err := workflowStanding(out)
	if err != nil {
		return err
	}
	return workflowGate(out, override, s, workflow.Landing(cfg, branch, "push"), workflow.PushRole(cfg, branch, s))
}
