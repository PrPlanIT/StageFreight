package forge

import "context"

// DefaultBranchSetter is the OPTIONAL capability a forge implements when it can change a
// repo's default branch — the branch visitors land on and merge requests target. Callers
// type-assert it and report a skip where a forge does not implement it.
type DefaultBranchSetter interface {
	SetDefaultBranch(ctx context.Context, branch string) error
}

// SetDefaultBranch sets the repo's default branch (GitHub: PATCH /repos/{o}/{r}).
func (g *GitHubForge) SetDefaultBranch(ctx context.Context, branch string) error {
	return g.doJSON(ctx, "PATCH", g.apiURL(""), map[string]any{"default_branch": branch}, nil)
}

// SetDefaultBranch sets the project's default branch (GitLab: PUT /projects/:id).
func (g *GitLabForge) SetDefaultBranch(ctx context.Context, branch string) error {
	return g.doJSON(ctx, "PUT", g.apiURL(""), map[string]any{"default_branch": branch}, nil)
}

// SetDefaultBranch sets the repo's default branch (Gitea/Forgejo: PATCH /repos/{o}/{r}).
func (g *GiteaForge) SetDefaultBranch(ctx context.Context, branch string) error {
	return g.doJSON(ctx, "PATCH", g.apiURL(""), map[string]any{"default_branch": branch}, nil)
}
