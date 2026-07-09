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
	if opts.Domain != "" {
		if err := os.WriteFile(filepath.Join(ws, "CNAME"), []byte(opts.Domain+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
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
	url := githubPagesURL(repo, opts.Domain)
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
	if err := r.PushContext(ctx, &git.PushOptions{
		RemoteName: "origin",
		RefSpecs:   []gitconfig.RefSpec{"+HEAD:refs/heads/gh-pages"},
		Auth:       &githttp.BasicAuth{Username: "x-access-token", Password: token},
		Force:      true,
	}); err != nil && err != git.NoErrAlreadyUpToDate {
		return DeployResult{}, fmt.Errorf("github pages: push: %w", err)
	}
	// GitHub's custom-domain model is the CNAME file written into the tree during
	// Prepare (not an API attach), so there's no separate DomainOutcome to report; the
	// returned URL already reflects opts.Domain when set.
	return DeployResult{URL: url}, nil
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
