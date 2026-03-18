package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/config"
)

var (
	cfgFile string
	verbose bool
	cfg     *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "stagefreight",
	Short: "DevOps automation CLI",
	Long:  "StageFreight — cache-aware, delta-only code quality and CI/CD automation.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for commands that don't need it.
		if cmd.Name() == "version" {
			return nil
		}
		var warnings []string
		var err error
		cfg, warnings, err = config.LoadWithWarnings(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "  warning: %s\n", w)
		}
		return nil
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: .stagefreight.yml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
}

// Execute runs the root command. All exit paths call os.Exit explicitly.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var silentErr *SilentExitError
		if errors.As(err, &silentErr) {
			os.Exit(silentErr.Code)
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, exitErr.Err)
			os.Exit(exitErr.Code)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
