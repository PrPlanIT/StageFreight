package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// configFreeCommands must gate on the TOP-LEVEL command only. cmd.Name() returns the
// leaf, so a bare-name match also skips config loading for any SUBCOMMAND sharing that
// name — `dependency update` leafs to "update", was treated as the binary self-updater,
// and ran with a nil cfg, segfaulting on its first config read.
func TestConfigFreeGateIsTopLevelOnly(t *testing.T) {
	var depUpdate, topUpdate *cobra.Command
	for _, c := range rootCmd.Commands() {
		switch c.Name() {
		case "update":
			topUpdate = c
		case "dependency":
			for _, sub := range c.Commands() {
				if sub.Name() == "update" {
					depUpdate = sub
				}
			}
		}
	}
	if topUpdate == nil || depUpdate == nil {
		t.Fatalf("fixture: topUpdate=%v depUpdate=%v", topUpdate != nil, depUpdate != nil)
	}
	// Both leaf to the same name — the collision this guards.
	if topUpdate.Name() != depUpdate.Name() {
		t.Fatalf("expected the name collision, got %q vs %q", topUpdate.Name(), depUpdate.Name())
	}
	isTopLevel := func(c *cobra.Command) bool { return c.Parent() != nil && c.Parent().Parent() == nil }
	if !(isTopLevel(topUpdate) && configFreeCommands[topUpdate.Name()]) {
		t.Error("top-level update must stay config-free: it replaces the binary that parses config")
	}
	if isTopLevel(depUpdate) && configFreeCommands[depUpdate.Name()] {
		t.Error("dependency update must NOT be config-free — it reads cfg.Dependency")
	}
}
