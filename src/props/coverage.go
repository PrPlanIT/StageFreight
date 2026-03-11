package props

import "fmt"

func init() {
	Register(Definition{
		ID:          "codecov",
		Format:      "badge",
		Category:    "quality",
		Description: "Code coverage from Codecov",
		Provider:    ProviderNative,
		DefaultAlt:  "Codecov",
		Resolver:    &codecovResolver{},
	})
}

type codecovResolver struct{}

func (r *codecovResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	repo := params["repo"]
	imgURL := fmt.Sprintf("https://codecov.io/gh/%s/branch/main/graph/badge.svg", repo)
	if branch, ok := params["branch"]; ok && branch != "" {
		imgURL = fmt.Sprintf("https://codecov.io/gh/%s/branch/%s/graph/badge.svg", repo, branch)
	}
	return ResolvedProp{
		ImageURL: imgURL,
		LinkURL:  fmt.Sprintf("https://codecov.io/gh/%s", repo),
	}, nil
}

func (r *codecovResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "repo", Required: true, Help: "GitHub owner/name (e.g. prplanit/stagefreight)"},
			{Name: "branch", Help: "Branch name (default: main)"},
		},
		Example: map[string]string{"repo": "prplanit/stagefreight", "branch": "main"},
	}
}
