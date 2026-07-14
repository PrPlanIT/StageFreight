package pages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// githubProvider deploys a publish workspace to GitHub Pages by force-pushing it to the
// gh-pages branch of the target repo (replace mode — one fresh commit overwrites the
// branch). Uses go-git, not the git binary: the runtime image has no git and shelling
// it is forbidden (the no-git-exec invariant). The token rides in BasicAuth, never in a
// remote URL or process arg.
type githubProvider struct{}

func (g *githubProvider) Credentials() []CredentialRequirement {
	return []CredentialRequirement{
		{Name: "GITHUB_TOKEN", Required: true, Description: "GitHub token with contents:write on the Pages repo"},
	}
}

// Prepare filters the workspace, then writes GitHub-specific metadata: .nojekyll (serve
// files verbatim, skip Jekyll processing) and CNAME (custom domain, if set). Provider
// quirks stay here rather than in the publish runner.
func (g *githubProvider) Prepare(ws string, opts DeployOpts) error {
	if err := FilterWorkspace(ws, opts); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ws, ".nojekyll"), nil, 0o644); err != nil {
		return err
	}
	// GitHub Pages serves a single custom domain (one CNAME record). Config validation
	// rejects a multi-domain list for the github provider, so at most one is set here;
	// firstDomain is a defensive fallback that also handles the empty case.
	if d := firstDomain(opts.Domains); d != "" {
		if err := os.WriteFile(filepath.Join(ws, "CNAME"), []byte(d+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// firstDomain returns the first non-empty domain, or "" when there are none.
func firstDomain(domains []string) string {
	for _, d := range domains {
		if d != "" {
			return d
		}
	}
	return ""
}

func (g *githubProvider) Deploy(ctx context.Context, ws string, opts DeployOpts) (DeployResult, error) {
	repo := opts.Repo
	if repo == "" {
		repo = os.Getenv("GITHUB_REPOSITORY") // "OWNER/REPO" on GitHub Actions
	}
	if repo == "" {
		return DeployResult{}, fmt.Errorf("github pages: target repo unknown — set the target's project_id (OWNER/REPO)")
	}
	token := opts.Env["GITHUB_TOKEN"]
	if token == "" {
		return DeployResult{}, fmt.Errorf("github pages: missing required credential GITHUB_TOKEN")
	}
	url := githubPagesURL(repo, firstDomain(opts.Domains))
	if opts.DryRun {
		return DeployResult{URL: fmt.Sprintf("[dry-run] would force-push %s to %s gh-pages", ws, repo)}, nil
	}

	// Init a repo over the workspace, commit everything, force-push HEAD → gh-pages.
	r, err := git.PlainInit(ws, false)
	if err != nil {
		return DeployResult{}, fmt.Errorf("github pages: git init: %w", err)
	}
	wt, err := r.Worktree()
	if err != nil {
		return DeployResult{}, fmt.Errorf("github pages: worktree: %w", err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return DeployResult{}, fmt.Errorf("github pages: staging: %w", err)
	}
	if _, err := wt.Commit("deploy via StageFreight", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "StageFreight",
			Email: "stagefreight@users.noreply.github.com",
			When:  time.Now(),
		},
	}); err != nil {
		return DeployResult{}, fmt.Errorf("github pages: commit: %w", err)
	}
	if _, err := r.CreateRemote(&gitconfig.RemoteConfig{
		Name: "origin",
		URLs: []string{fmt.Sprintf("https://github.com/%s.git", repo)},
	}); err != nil {
		return DeployResult{}, fmt.Errorf("github pages: remote: %w", err)
	}
	// Resolve HEAD to a CONCRETE branch ref. go-git does not resolve a symbolic "HEAD"
	// refspec SOURCE on push — it matches nothing, pushes nothing, and returns
	// NoErrAlreadyUpToDate, which was swallowed as success and surfaced a false
	// "deployed" with no gh-pages branch ever created. Push the branch HEAD points at.
	head, err := r.Head()
	if err != nil {
		return DeployResult{}, fmt.Errorf("github pages: resolve HEAD: %w", err)
	}
	auth := &githttp.BasicAuth{Username: "x-access-token", Password: token}
	if err := r.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{gitconfig.RefSpec(fmt.Sprintf("+%s:refs/heads/gh-pages", head.Name()))},
		Auth:       auth,
		Force:      true,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return DeployResult{}, fmt.Errorf("github pages: push: %w", err)
	}

	// A reported deploy must be REAL: go-git can no-op a push and still return without a
	// hard error, so confirm the remote gh-pages now points at the commit we built.
	// Never report success for a branch that wasn't actually created/updated.
	if err := verifyGHPagesRef(ctx, r, auth, head.Hash()); err != nil {
		return DeployResult{}, err
	}

	// GitHub's custom-domain model is the CNAME file written into the tree during
	// Prepare (not an API attach), so there's no separate DomainOutcome to report; the
	// returned URL already reflects the custom domain when set.
	return DeployResult{URL: url}, nil
}

// verifyGHPagesRef confirms the remote gh-pages branch now points at want, so a deploy is
// only reported successful when the push genuinely landed — a "deployed" message must
// never outrun the actual remote state.
func verifyGHPagesRef(ctx context.Context, r *git.Repository, auth *githttp.BasicAuth, want plumbing.Hash) error {
	remote, err := r.Remote("origin")
	if err != nil {
		return fmt.Errorf("github pages: verify: %w", err)
	}
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth})
	if err != nil {
		return fmt.Errorf("github pages: verify (list remote refs): %w", err)
	}
	for _, ref := range refs {
		if ref.Name() == plumbing.ReferenceName("refs/heads/gh-pages") {
			if ref.Hash() == want {
				return nil
			}
			return fmt.Errorf("github pages: push did not land — remote gh-pages is %s, expected %s", ref.Hash().String()[:8], want.String()[:8])
		}
	}
	return fmt.Errorf("github pages: push returned no error but gh-pages does not exist on the remote — nothing was deployed (likely an unresolved refspec or a rejected push)")
}

func githubPagesURL(repo, domain string) string {
	if domain != "" {
		return "https://" + domain + "/"
	}
	if owner, name, ok := strings.Cut(repo, "/"); ok {
		return fmt.Sprintf("https://%s.github.io/%s/", owner, name)
	}
	return ""
}
