package props

import "fmt"

func init() {
	Register(Definition{
		ID:          "github-actions",
		Format:      "badge",
		Category:    "ci",
		Description: "GitHub Actions workflow status",
		Provider:    ProviderNative,
		DefaultAlt:  "Build Status",
		Resolver:    &githubActionsResolver{},
	})

	Register(Definition{
		ID:          "circleci",
		Format:      "badge",
		Category:    "ci",
		Description: "CircleCI build status",
		Provider:    ProviderNative,
		DefaultAlt:  "CircleCI",
		Resolver:    &circleciResolver{},
	})
}

type githubActionsResolver struct{}

func (r *githubActionsResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	repo := params["repo"]
	workflow := params["workflow"]

	imgURL := fmt.Sprintf("https://github.com/%s/actions/workflows/%s/badge.svg", repo, workflow)
	if branch, ok := params["branch"]; ok && branch != "" {
		imgURL += fmt.Sprintf("?branch=%s", branch)
	}
	if event, ok := params["event"]; ok && event != "" {
		sep := "?"
		if _, hasBranch := params["branch"]; hasBranch && params["branch"] != "" {
			sep = "&"
		}
		imgURL += fmt.Sprintf("%sevent=%s", sep, event)
	}

	return ResolvedProp{
		ImageURL: imgURL,
		LinkURL:  fmt.Sprintf("https://github.com/%s/actions/workflows/%s", repo, workflow),
	}, nil
}

func (r *githubActionsResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "repo", Required: true, Help: "GitHub owner/name (e.g. prplanit/stagefreight)"},
			{Name: "workflow", Required: true, Help: "Workflow filename (e.g. build.yml)"},
			{Name: "branch", Help: "Branch filter"},
			{Name: "event", Help: "Event filter (push, pull_request, etc.)"},
		},
		Example: map[string]string{
			"repo":     "prplanit/stagefreight",
			"workflow": "build.yml",
			"branch":   "main",
		},
	}
}

type circleciResolver struct{}

func (r *circleciResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	repo := params["repo"]
	vcs := "gh"
	if v, ok := params["vcs"]; ok && v != "" {
		vcs = v
	}

	imgURL := fmt.Sprintf("https://dl.circleci.com/status-badge/img/%s/%s/tree/main.svg", vcs, repo)
	if branch, ok := params["branch"]; ok && branch != "" {
		imgURL = fmt.Sprintf("https://dl.circleci.com/status-badge/img/%s/%s/tree/%s.svg", vcs, repo, branch)
	}

	return ResolvedProp{
		ImageURL: imgURL,
		LinkURL:  fmt.Sprintf("https://dl.circleci.com/status-badge/redirect/%s/%s/tree/main", vcs, repo),
	}, nil
}

func (r *circleciResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "repo", Required: true, Help: "Org/repo path (e.g. prplanit/stagefreight)"},
			{Name: "branch", Help: "Branch name (default: main)"},
			{Name: "vcs", Default: "gh", Help: "VCS provider: gh (GitHub) or bb (Bitbucket)"},
		},
		Example: map[string]string{"repo": "prplanit/stagefreight"},
	}
}
