package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/test"
	"github.com/spf13/cobra"
)

var testGate string

var testCmd = &cobra.Command{
	Use:   "test [suite-id...]",
	Short: "Run the project's test suites (go test / cargo test / custom)",
	Long: `Run StageFreight test suites locally — the SAME suites, resolved the same way,
the CI audition phase runs. No arguments runs all suites; pass suite ids to run a
subset, or --gate to filter by lifecycle tier.

Suites are auto-synthesized from your builds when none are declared in .stagefreight.yml
(a go builder → "go test ./..."; a rust builder → "cargo test").`,
	RunE: runTestCmd,
}

func init() {
	testCmd.Flags().StringVar(&testGate, "gate", "", "run only suites with this gate (perform|advisory)")
	rootCmd.AddCommand(testCmd)
}

func runTestCmd(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	suites, err := test.Resolve(cfg, rootDir)
	if err != nil {
		return err
	}
	suites = filterSuites(suites, args, testGate)
	if len(suites) == 0 {
		fmt.Println("  test: no suites to run")
		return nil
	}
	res := test.RunRender(context.Background(), suites, rootDir, cfg.Toolchains.Desired, os.Stdout, test.IntentCorrectness)
	if res.Failed() {
		// Local convenience: non-zero exit, already rendered.
		return silentExit(fmt.Errorf("tests failed"))
	}
	return nil
}

// auditionTests runs the resolved suites during the audition phase and gates the
// pipeline. A failed NON-ADVISORY suite returns an error → audition exits non-zero
// → its `.stagefreight/` (cistate) artifact is withheld (GitLab `on_success`) →
// every downstream phase's `assertAuditionRan` halts (only narrate runs). This is
// the exact gate a lint critical uses — no new authorization wiring. Advisory
// suites render red but never affect the return.
func auditionTests(ctx context.Context, appCfg *config.Config, rootDir string) error {
	passed, err := test.Verify(ctx, appCfg, rootDir, os.Stdout, test.IntentCorrectness)
	if err != nil {
		return fmt.Errorf("test subsystem: %w", err)
	}
	if !passed {
		return silentExit(fmt.Errorf("test: one or more gating suites failed"))
	}
	return nil
}

// filterSuites selects by explicit suite ids (if any) and/or gate tier.
func filterSuites(suites []test.ResolvedSuite, ids []string, gate string) []test.ResolvedSuite {
	idset := map[string]bool{}
	for _, id := range ids {
		idset[id] = true
	}
	out := make([]test.ResolvedSuite, 0, len(suites))
	for _, s := range suites {
		if len(idset) > 0 && !idset[s.ID] {
			continue
		}
		if gate != "" && string(s.Gate) != gate {
			continue
		}
		out = append(out, s)
	}
	return out
}

