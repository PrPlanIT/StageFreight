package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/ci"
)

var ciRunTag string

var ciRunCmd = &cobra.Command{
	Use:   "run <subsystem>",
	Short: "Run a CI subsystem",
	Long: fmt.Sprintf(`Run a CI subsystem by name.

Valid subsystems: %s

Provider skeletons set SF_CI_* environment variables, then call this command.
Subsystem behavior is configured in .stagefreight.yml.

Exit codes: 0=success, 1=subsystem error, 2=config error, 3=context error`, strings.Join(ci.ValidSubsystems(), ", ")),
	Args: cobra.ExactArgs(1),
	RunE: runCIRun,
}

func init() {
	ciRunCmd.Flags().StringVar(&ciRunTag, "tag", "", "release tag (overrides SF_CI_TAG for release subsystem)")

	ciCmd.AddCommand(ciRunCmd)
}

func runCIRun(cmd *cobra.Command, args []string) error {
	subsystem := args[0]
	ctx := context.Background()

	// Resolve CI context from SF_CI_* env vars (with git fallbacks)
	ciCtx := ci.ResolveContext()

	opts := ci.RunOptions{
		Tag:     ciRunTag,
		Verbose: verbose,
	}

	registry := buildCIRegistry()

	if err := ci.RunSubsystem(registry, subsystem, ctx, cfg, ciCtx, opts); err != nil {
		return err
	}

	return nil
}
