package dependency

import (
	"io"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// UpdateConfig holds configuration for the dependency update command.
type UpdateConfig struct {
	RootDir    string
	OutputDir  string // default ".stagefreight/deps/" — overwrites existing artifacts
	DryRun     bool
	Bundle     bool         // generate .tgz
	Verify     bool         // run tests after update (default true)
	Vulncheck  bool         // run govulncheck after update (default true)
	Ecosystems []string     // filter by ecosystem (empty = all)
	Policy     string       // freshness SCOPE: "all" (default), "security"
	MaxUpdate  string       // update-type CEILING: "major" | "minor" (default) | "patch"
	Ignore     []VulnIgnore // accepted-risk advisory suppressions (id/reason/until)
	Writer     io.Writer    // section-card output target (default os.Stderr); the deps progress card renders here
}

// VulnIgnore is a single accepted-risk advisory suppression. Mirrors config's
// DependencyIgnore (mapped by the CLI) so the dependency engine stays config-agnostic.
// VulnIgnore is the shared advisory-exception record. Aliased rather than redefined so
// dependency and lint cannot drift on what "ignored" or "expired" means.
type VulnIgnore = supplychain.VulnIgnore
