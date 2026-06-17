package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign/anchordoc"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

var signingCmd = &cobra.Command{
	Use:   "signing",
	Short: "Signing identity + trust-anchor maintenance",
}

var (
	signingAnchorFile   string
	signingAnchorConfig string
)

var signingAnchorCmd = &cobra.Command{
	Use:   "anchor",
	Short: "Regenerate the canonical signing trust anchor (managed SECURITY.md section)",
	Long: `Regenerates the managed signing-anchor section — the stable, committed, canonical
trust anchor that per-release Verification sections reference.

It updates ONLY the marked section (between <!-- sf:signing-anchor:start --> and
<!-- sf:signing-anchor:end -->), preserving all surrounding operator-authored
security prose. Deterministic and idempotent. This is an explicit docs-generation
step: it never runs during publish and never mutates the repo silently.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		rootDir, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg, err := config.Load(signingAnchorConfig)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		stateDir, err := cfg.SigningSetup.StateDir.Resolve()
		if err != nil {
			return err
		}
		if stateDir == "" {
			return fmt.Errorf("signing.state_dir is not configured — there is no auto-provisioned identity to anchor")
		}
		if err := provision.GuardStateDir(stateDir, rootDir); err != nil {
			return err
		}
		id, err := provision.LoadIdentity(stateDir)
		if err != nil {
			return err
		}
		if id == nil {
			return fmt.Errorf("no signing identity has been provisioned in %s yet (run a build with signing.auto_provision: true first)", stateDir)
		}

		pub, err := os.ReadFile(id.PubPath(stateDir))
		if err != nil {
			return fmt.Errorf("reading public key: %w", err)
		}

		file := signingAnchorFile
		if !filepath.IsAbs(file) {
			file = filepath.Join(rootDir, file)
		}
		if err := anchordoc.Update(file, anchordoc.Render(id, string(pub))); err != nil {
			return fmt.Errorf("updating %s: %w", signingAnchorFile, err)
		}

		fmt.Printf("✓ signing anchor updated in %s\n", signingAnchorFile)
		fmt.Printf("  tier:        %s\n", id.Tier)
		fmt.Printf("  fingerprint: %s\n", id.Fingerprint)
		fmt.Printf("  (commit %s to publish the canonical trust anchor)\n", signingAnchorFile)
		return nil
	},
}

func init() {
	signingAnchorCmd.Flags().StringVar(&signingAnchorFile, "file", "SECURITY.md", "file whose managed signing-anchor section to update")
	signingAnchorCmd.Flags().StringVar(&signingAnchorConfig, "config", ".stagefreight.yml", "config file")
	signingCmd.AddCommand(signingAnchorCmd)
	rootCmd.AddCommand(signingCmd)
}
