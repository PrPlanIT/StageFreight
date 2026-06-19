package dependency

import "io"

// UpdateConfig holds configuration for the dependency update command.
type UpdateConfig struct {
	RootDir    string
	OutputDir  string // default ".stagefreight/deps/" — overwrites existing artifacts
	DryRun     bool
	Bundle     bool      // generate .tgz
	Verify     bool      // run tests after update (default true)
	Vulncheck  bool      // run govulncheck after update (default true)
	Ecosystems []string  // filter by ecosystem (empty = all)
	Policy     string    // "all" (default), "security"
	Writer     io.Writer // section-card output target (default os.Stderr); the deps progress card renders here
}
