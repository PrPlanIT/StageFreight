package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/ci/render"
)

var (
	ciRenderWrite bool
	ciRenderCheck bool
)

var ciRenderCmd = &cobra.Command{
	Use:       "render [forge]",
	Short:     "Render forge-native CI pipeline from .stagefreight.yml",
	ValidArgs: render.SupportedForges,
	Args:      cobra.MaximumNArgs(1),
	RunE:      runCIRender,
}

func init() {
	// Help text is derived from render.SupportedForges so it can never drift from
	// what the dispatcher actually supports — it previously claimed gitlab-only
	// while the code shipped gitlab/github/gitea/forgejo/azuredevops.
	ciRenderCmd.Long = fmt.Sprintf(`Generate a forge-native CI pipeline file from StageFreight configuration.

Supported forges: %s  (azuredevops is experimental)

The rendered file is a committed generated artifact. StageFreight owns the
pipeline document — it is not hand-maintained.

Modes:
  --write   Write the rendered pipeline to the repo (e.g. .gitlab-ci.yml)
  --check   Verify the committed pipeline matches what would be rendered (exit 1 if stale)
  (default) Print the rendered pipeline to stdout

The forge argument is optional when ci.forge: declares it — a repo has already
said where its CI runs, and making it repeat that on every invocation invites the
two to disagree. Passing one renders that forge only; with several declared and
--write or --check, all of them are handled.`, strings.Join(render.SupportedForges, ", "))

	ciRenderCmd.Flags().BoolVar(&ciRenderWrite, "write", false, "write rendered pipeline to repo")
	ciRenderCmd.Flags().BoolVar(&ciRenderCheck, "check", false, "verify committed pipeline is up to date")

	ciCmd.AddCommand(ciRenderCmd)
}

func runCIRender(_ *cobra.Command, args []string) error {
	forges, err := resolveRenderForges(args)
	if err != nil {
		return err
	}

	p, err := render.Plan(cfg)
	if err != nil {
		return err
	}

	// Printing to stdout is a single-document operation — concatenating several
	// pipelines produces a file that is valid for no forge — so it requires one forge.
	if !ciRenderCheck && !ciRenderWrite && len(forges) > 1 {
		return fmt.Errorf("ci.forge declares %s: name one to print, or use --write/--check to handle all",
			strings.Join(forges, ", "))
	}

	for _, forge := range forges {
		rendered, err := render.Emit(forge, p)
		if err != nil {
			return err
		}
		target, _ := render.ForgeTarget(forge)

		switch {
		case ciRenderCheck:
			if err := render.Check(".", forge, rendered); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "%s is up to date\n", target)
		case ciRenderWrite:
			path := filepath.Join(".", target)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("creating directory for %s: %w", target, err)
			}
			if err := os.WriteFile(path, rendered, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", target, err)
			}
			fmt.Fprintf(os.Stderr, "wrote %s\n", target)
		default:
			if _, err := os.Stdout.Write(rendered); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveRenderForges decides which forges to render: the argument when given,
// otherwise what ci.forge: declares. An explicit argument wins so a one-off render
// stays possible, and the absence of both is an error rather than a guess — rendering
// a pipeline for a forge the repo never named is how a skeleton ends up committed for
// a CI system that does not run it.
func resolveRenderForges(args []string) ([]string, error) {
	if len(args) == 1 {
		return args[:1], nil
	}
	if cfg != nil && len(cfg.CI.Forge) > 0 {
		return cfg.CI.Forge, nil
	}
	return nil, fmt.Errorf("no forge given and ci.forge: is not declared — pass one (%s) or declare ci.forge",
		strings.Join(render.SupportedForges, ", "))
}
