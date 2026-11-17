package forge

import "context"

// ForkParentReader is the OPTIONAL capability a forge implements when it records which
// project a repository was forked from. ForkParent returns the parent's clone URL, or ""
// when the repository is not a fork. Callers type-assert it and report a forge that does
// not implement it.
type ForkParentReader interface {
	ForkParent(ctx context.Context) (string, error)
}

// ForkParent reads the repo's parent (GitHub: GET /repos/{o}/{r} → parent.clone_url).
func (g *GitHubForge) ForkParent(ctx context.Context) (string, error) {
	var resp struct {
		Parent *struct {
			CloneURL string `json:"clone_url"`
		} `json:"parent"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &resp); err != nil {
		return "", err
	}
	if resp.Parent == nil {
		return "", nil
	}
	return resp.Parent.CloneURL, nil
}

// ForkParent reads the project's parent (GitLab: GET /projects/:id →
// forked_from_project.http_url_to_repo).
func (g *GitLabForge) ForkParent(ctx context.Context) (string, error) {
	var resp struct {
		ForkedFrom *struct {
			HTTPURL string `json:"http_url_to_repo"`
		} `json:"forked_from_project"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &resp); err != nil {
		return "", err
	}
	if resp.ForkedFrom == nil {
		return "", nil
	}
	return resp.ForkedFrom.HTTPURL, nil
}

// ForkParent reads the repo's parent (Gitea/Forgejo: GET /repos/{o}/{r} → parent.clone_url).
func (g *GiteaForge) ForkParent(ctx context.Context) (string, error) {
	var resp struct {
		Parent *struct {
			CloneURL string `json:"clone_url"`
		} `json:"parent"`
	}
	if err := g.doJSON(ctx, "GET", g.apiURL(""), nil, &resp); err != nil {
		return "", err
	}
	if resp.Parent == nil {
		return "", nil
	}
	return resp.Parent.CloneURL, nil
}

// ForkParent reads the repository's parent (Azure DevOps: GET the repository →
// parentRepository.remoteUrl).
func (a *AzureDevOpsForge) ForkParent(ctx context.Context) (string, error) {
	var resp struct {
		Parent *struct {
			RemoteURL string `json:"remoteUrl"`
		} `json:"parentRepository"`
	}
	if err := a.doJSON(ctx, "GET", a.gitURL("", ""), nil, &resp); err != nil {
		return "", err
	}
	if resp.Parent == nil {
		return "", nil
	}
	return resp.Parent.RemoteURL, nil
}
