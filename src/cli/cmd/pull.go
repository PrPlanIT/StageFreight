package cmd

import (
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/commit"
	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/spf13/cobra"
)

var (
	pullRemote string
	pullYes    bool
)

func init() {
	pullCmd.Flags().StringVar(&pullRemote, "remote", "origin", "git remote to pull from")
	pullCmd.Flags().BoolVar(&pullYes, "yes", false, "approve a rebase-onto-remote (diverged branch) without prompting")
	rootCmd.AddCommand(pullCmd)
}

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Plan and bring the remote's commits into the current branch",
	Long: `Pull the current branch's remote into your local branch.

StageFreight shows the plan and executes it: fast-forward when you're behind, or rebase
your local commits onto the remote (with your confirmation) when the branch has diverged.
It refuses on a mid-flight git operation rather than acting on a half-finished state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving working directory: %w", err)
		}
		return runPlanned(rootDir, pullRemote, pullYes, os.Stdout, func(e *commit.Engine, p gitplan.Policy) (gitplan.Plan, error) {
			return e.PlanPull(p), nil
		})
	},
}
