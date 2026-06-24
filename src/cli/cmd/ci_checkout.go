package cmd

import (
	"context"
	"fmt"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
)

var ciCheckoutCmd = &cobra.Command{
	Use:   "checkout",
	Short: "Materialize the repository workspace via go-git (no git binary required)",
	Long: `Clone the CI repository into the workspace using the embedded go-git transport.

On Actions-family container jobs (GitHub/Gitea/Forgejo) the checkout runs INSIDE the
StageFreight image, which carries no git binary — so actions/checkout would fall back
to a .git-less REST tarball, breaking every git-aware subsystem. This command clones
the repo itself (auth resolved from GITHUB_TOKEN etc. via the standard resolver) so a
real .git is present. go-git is ownership-agnostic, so no safe.directory is required.

GitLab does not use this — its runner clones at the runner level and the container
inherits the .git.

Exit codes: 0=success, 1=checkout error, 3=context error`,
	Args: cobra.NoArgs,
	RunE: runCICheckout,
}

func init() {
	ciCmd.AddCommand(ciCheckoutCmd)
}

// runCICheckout is the CI bootstrap orchestrator: it interprets the CI context and
// assembles the proven gitstate/go-git primitives (auth resolver + clone) into a
// workspace materialization. It is deliberately NOT a gitstate API — there is one
// consumer and the behavior is CI-bootstrap-specific.
func runCICheckout(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	ciCtx := ci.ResolveContext()

	if ciCtx.RepoURL == "" {
		return fmt.Errorf("checkout: SF_CI_REPO_URL is empty — no repository to clone")
	}
	dir := ciCtx.Workspace
	if dir == "" {
		dir = "."
	}

	// Resolve the CI ref: a tag build clones the tag, otherwise the branch. Single-
	// branch keeps the clone to that ref's history (the SHA lives in it).
	var ref plumbing.ReferenceName
	switch {
	case ciCtx.Tag != "":
		ref = plumbing.NewTagReferenceName(ciCtx.Tag)
	case ciCtx.Branch != "":
		ref = plumbing.NewBranchReferenceName(ciCtx.Branch)
	}

	auth, err := gitstate.ResolveHTTPAuth(ciCtx.RepoURL)
	if err != nil {
		return fmt.Errorf("checkout: resolving auth: %w", err)
	}

	cloneOpts := &git.CloneOptions{URL: ciCtx.RepoURL, Auth: auth}
	if ref != "" {
		cloneOpts.ReferenceName = ref
		cloneOpts.SingleBranch = true
	}

	fmt.Printf("  checkout: cloning %s (%s) via go-git\n", ciCtx.RepoURL, ref)
	repo, err := git.PlainCloneContext(ctx, dir, false, cloneOpts)
	if err != nil {
		return fmt.Errorf("checkout: clone %s: %w", ciCtx.RepoURL, err)
	}

	// Pin the exact commit — CI runs at a specific SHA, which may trail the ref tip
	// by the time the job starts.
	if ciCtx.SHA != "" {
		wt, err := repo.Worktree()
		if err != nil {
			return fmt.Errorf("checkout: worktree: %w", err)
		}
		if err := wt.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(ciCtx.SHA)}); err != nil {
			return fmt.Errorf("checkout: pinning %s: %w", ciCtx.SHA, err)
		}
	}

	fmt.Println("  checkout: workspace materialized")
	return nil
}
