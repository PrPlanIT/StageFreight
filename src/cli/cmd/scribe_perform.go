package cmd

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/scribe"
)

// renderBuildFedScribe composes one build-fed scribe content item during PERFORM, at
// its DAG slot before the build that bakes it (wired onto RunContext.RenderScribeItem).
// It (1) materializes the item's upstream build's worktree output into the tree — so
// the compose reads THIS run's generated content, not the last commit's — then (2)
// renders ONLY that item's files, leaving late items (badges) for the publish pass.
// The commit still happens once, in publish; this only makes the composed pages exist
// in time for a consuming build.
func renderBuildFedScribe(_ context.Context, appCfg *config.Config, rootDir, itemID string, verbose bool) error {
	def, ok := appCfg.StencilsByID()[itemID]
	if !ok {
		return fmt.Errorf("scribe item %q not found in scribe.content", itemID)
	}
	if def.Build != "" {
		for _, b := range appCfg.Builds {
			if b.ID != def.Build {
				continue
			}
			for _, o := range b.Outputs {
				if o.LandsInWorktree() {
					if err := landBuildTree(rootDir, b.ID, o.WorktreePath()); err != nil {
						return fmt.Errorf("materializing build %q for scribe item %q: %w", b.ID, itemID, err)
					}
				}
			}
		}
	}
	if _, err := scribe.RunItems(appCfg, rootDir, []string{itemID}, false, verbose); err != nil {
		return fmt.Errorf("composing scribe item %q: %w", itemID, err)
	}
	return nil
}
