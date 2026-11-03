package cmd

import (
	"context"
	"fmt"
	"strings"

	git "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/spf13/cobra"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/config"
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

	// Resolve the CI ref the event points at. SF_CI_REF, when the forge supplies it, is
	// authoritative — it is the exact ref the run is for (refs/heads/x on a push, refs/tags/x
	// on a tag, refs/pull/N/merge on a pull request). We only need its branch/tag SHAPE to
	// choose a clone strategy; the pull-request case can't be reconstructed from a branch
	// name (ref_name is "<N>/merge", which is not a branch), which is why it must be carried
	// as a full ref and fetched as one.
	fullRef := plumbing.ReferenceName(strings.TrimSpace(ciCtx.Ref))
	var ref plumbing.ReferenceName
	switch {
	case ciCtx.Tag != "":
		ref = plumbing.NewTagReferenceName(ciCtx.Tag)
	case ciCtx.Branch != "":
		ref = plumbing.NewBranchReferenceName(ciCtx.Branch)
	}

	// Checkout runs BEFORE the repo (and its .stagefreight.yml forge graph) exists, so the
	// credential is resolved host-bound from the CI context itself: a synthetic forge for
	// the CI provider and the repo URL, whose credential prefix is the provider-native token
	// the runner exports (GITHUB_TOKEN / GITLAB_TOKEN / GITEA_TOKEN / FORGEJO_TOKEN, or the
	// GitLab CI job token). ResolveGitCredential binds it to the repo's host — the CI
	// provider's own host — so a mirror job's OTHER forge token is never offered here.
	bootstrapCfg := &config.Config{
		Forges: []config.ForgeConfig{{
			Provider:    ciCtx.Provider,
			URL:         ciCtx.RepoURL,
			Credentials: strings.ToUpper(ciCtx.Provider),
		}},
	}
	auth, err := gitstate.ResolveGitCredential(ciCtx.RepoURL, bootstrapCfg)
	if err != nil {
		return fmt.Errorf("checkout: resolving auth: %w", err)
	}

	// A pull-request run points at a ref that is neither a branch nor a tag
	// (refs/pull/N/merge). go-git's single-branch clone builds its fetch refspec as
	// "+refs/heads/<short>:…" — it cannot fetch a pull ref, so cloning it as a branch
	// fails with "couldn't find remote ref refs/heads/N/merge". Fetch the ref explicitly
	// by its full name instead, then pin the SHA. The forge advertises the pull ref
	// (GitHub/Gitea/Forgejo all do), so this resolves the merge commit the run is for.
	if needsExplicitRefFetch(fullRef) {
		return checkoutExplicitRef(ctx, dir, ciCtx, auth, fullRef)
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

// needsExplicitRefFetch reports whether a full ref must be fetched by its explicit
// refspec rather than cloned as a branch/tag. True only for a non-empty ref that is
// neither a branch (refs/heads/*) nor a tag (refs/tags/*) — in practice a pull-request
// merge ref (refs/pull/N/merge), which go-git's single-branch clone cannot fetch. A
// branch/tag ref, or an empty ref (forges that don't export SF_CI_REF), takes the
// proven clone path driven by ciCtx.Branch/Tag.
func needsExplicitRefFetch(ref plumbing.ReferenceName) bool {
	return ref != "" && !ref.IsBranch() && !ref.IsTag()
}

// checkoutExplicitRef materializes a workspace from a full ref that go-git's clone
// cannot fetch as a branch or tag — a pull-request merge ref (refs/pull/N/merge). It
// initializes an empty repo, fetches exactly that ref by its full name, and checks out
// the run's SHA (the merge commit the forge advertises as the ref tip). A pull run
// always carries SHA; if it is somehow absent we fall back to the fetched ref tip.
func checkoutExplicitRef(ctx context.Context, dir string, ciCtx *ci.CIContext, auth transport.AuthMethod, fullRef plumbing.ReferenceName) error {
	fmt.Printf("  checkout: cloning %s (%s) via go-git\n", ciCtx.RepoURL, fullRef)

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return fmt.Errorf("checkout: init %s: %w", dir, err)
	}
	remote, err := repo.CreateRemote(&gogitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{ciCtx.RepoURL},
	})
	if err != nil {
		return fmt.Errorf("checkout: remote: %w", err)
	}

	// Fetch the pull ref by its full name into a local ref of the same name — mirroring it
	// 1:1 rather than mapping through refs/heads or refs/remotes, which the pull ref has no
	// place in. The run's SHA lives at this ref's tip.
	refspec := gogitconfig.RefSpec(fmt.Sprintf("+%s:%s", fullRef, fullRef))
	if err := remote.FetchContext(ctx, &git.FetchOptions{
		RefSpecs: []gogitconfig.RefSpec{refspec},
		Auth:     auth,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return fmt.Errorf("checkout: fetch %s: %w", fullRef, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("checkout: worktree: %w", err)
	}

	co := &git.CheckoutOptions{}
	if ciCtx.SHA != "" {
		co.Hash = plumbing.NewHash(ciCtx.SHA)
	} else {
		fetched, refErr := repo.Reference(fullRef, true)
		if refErr != nil {
			return fmt.Errorf("checkout: resolving %s: %w", fullRef, refErr)
		}
		co.Hash = fetched.Hash()
	}
	if err := wt.Checkout(co); err != nil {
		return fmt.Errorf("checkout: pinning %s: %w", co.Hash, err)
	}

	fmt.Println("  checkout: workspace materialized")
	return nil
}
