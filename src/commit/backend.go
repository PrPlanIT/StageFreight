package commit

import "context"

// Backend executes a commit plan.
type Backend interface {
	Execute(ctx context.Context, plan *Plan, conventional bool) (*Result, error)
}

// Result holds the outcome of a commit execution.
// All informational status must be represented here, not printed by backends.
type Result struct {
	SHA     string
	Message string
	Files   []string // actual staged files (from git diff --cached --name-only)
	Pushed  bool
	NoOp    bool
	Backend string      // stable descriptor: "git", "forge (gitlab)", "forge (github)", "dry-run"
	Sync    *SyncResult // populated when push was executed via the convergence engine

	// MaintainerOverride is true when --maintainer-override was active for this commit.
	// OverriddenBlocks records which governance checks were bypassed.
	// Both are empty for normal commits with no governance failures.
	MaintainerOverride bool
	OverriddenBlocks   CommitBlocks
}
