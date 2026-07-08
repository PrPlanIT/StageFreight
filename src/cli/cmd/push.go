package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/spf13/cobra"
)

var (
	pushRemote   string
	pushRefspec  string
	pushNoRebase bool
	pushYes      bool
)

func init() {
	pushCmd.Flags().StringVar(&pushRemote, "remote", "origin", "git remote to push to")
	pushCmd.Flags().StringVar(&pushRefspec, "refspec", "", "push refspec (e.g. HEAD:refs/heads/main) — uses the legacy convergence path")
	pushCmd.Flags().BoolVar(&pushNoRebase, "no-rebase", false, "legacy: fail instead of rebasing on a diverged branch (refspec path only)")
	pushCmd.Flags().BoolVar(&pushYes, "yes", false, "approve a transformational plan (e.g. replay onto a protected branch) without prompting")
	rootCmd.AddCommand(pushCmd)
}

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Plan and push the current branch to its remote",
	Long: `Push the current branch to its remote.

StageFreight resolves the repository state and destination into a plan, shows it, and
executes it — uploading, creating tracking, fast-forwarding, or (for a protected
destination) replaying with your confirmation. It never silently rewrites a feature branch.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		// Explicit --refspec (CI detached-HEAD fast path) keeps the legacy convergence
		// engine until the planner learns explicit destinations.
		if pushRefspec != "" {
			return legacyPush(rootDir)
		}
		return runPlannerPush(rootDir, pushRemote, pushYes, os.Stdout)
	},
}

// runPlannerPush drives the planner flow: fetch → Plan → Render (always shown) → Execute
// (gated on the plan's interaction ops). Extracted from the command so it is testable on a
// scratch repo without cobra/chdir.
func runPlannerPush(rootDir, remote string, approved bool, out io.Writer) error {
	session, err := gitstate.OpenSyncSession(rootDir)
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
	// Minimal policy for now (config-driven protected list is a follow-on).
	policy := gitplan.Policy{Protected: []string{"main", "master"}}
	plan := eng.Plan(policy)
	fmt.Fprint(out, gitplan.Render(plan))

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

// legacyPush is the pre-planner convergence path (engine.Sync). It is TRANSITIONAL DEBT:
// `commit --push` and `stagefreight push --refspec` still run it, so two "push" behaviors
// coexist and can drift — the exact divergence the planner exists to prevent. Retire it
// once the planner learns the explicit-destination + detached-HEAD/refspec case; then
// `commit --push` and this both route through Plan/Execute, and engine.Sync is deleted.
// Tracked in the git-planner plan (Slice 6: converge commit --push; Slice 7: delete Sync).
func legacyPush(rootDir string) error {
	opts := commit.PushOptions{
		Enabled:         true,
		Remote:          pushRemote,
		Refspec:         pushRefspec,
		RebaseOnDiverge: !pushNoRebase,
	}
	backend := &commit.GitBackend{RootDir: rootDir}
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
