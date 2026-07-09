package pages

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// githubProvider deploys a publish workspace to GitHub Pages by force-pushing it to the
// gh-pages branch of the target repo (replace mode — a single fresh commit). Runs git
// in the workspace. The token is passed via git's env-based config (GIT_CONFIG_*), not
// the remote URL or command args, so it never appears in the process table.
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

func (g *githubProvider) Deploy(ctx context.Context, ws string, opts DeployOpts) (string, error) {
	repo := opts.Repo
	if repo == "" {
		repo = os.Getenv("GITHUB_REPOSITORY") // "OWNER/REPO" on GitHub Actions
	}
	if repo == "" {
		return "", fmt.Errorf("github pages: target repo unknown — set the target's project_id (OWNER/REPO)")
	}
	token := opts.Env["GITHUB_TOKEN"]
	if token == "" {
		return "", fmt.Errorf("github pages: missing required credential GITHUB_TOKEN")
	}
	url := githubPagesURL(repo, opts.Domain)
	if opts.DryRun {
		return fmt.Sprintf("[dry-run] would force-push %s to %s gh-pages", ws, repo), nil
	}

	// Auth via env-based git config so the token is never in argv or the remote URL.
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	gitEnv := append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+auth,
		"GIT_TERMINAL_PROMPT=0",
	)
	remote := fmt.Sprintf("https://github.com/%s.git", repo)

	steps := [][]string{
		{"init", "-q"},
		{"config", "user.email", "stagefreight@users.noreply.github.com"},
		{"config", "user.name", "StageFreight"},
		{"checkout", "-q", "-b", "gh-pages"},
		{"add", "-A"},
		{"commit", "-q", "-m", "deploy via StageFreight"},
		{"push", "-q", "--force", remote, "gh-pages"},
	}
	for _, s := range steps {
		cmd := exec.CommandContext(ctx, "git", s...)
		cmd.Dir = ws
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("github pages: git %s failed: %w\n%s", s[0], err, string(out))
		}
	}
	return url, nil
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
