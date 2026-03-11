package props

import "fmt"

func init() {
	Register(Definition{
		ID:          "go-report-card",
		Format:      "badge",
		Category:    "quality",
		Description: "Go Report Card grade",
		Provider:    ProviderNative,
		DefaultAlt:  "Go Report Card",
		Resolver:    &goReportCardResolver{},
	})

	Register(Definition{
		ID:          "go-reference",
		Format:      "badge",
		Category:    "quality",
		Description: "Go package reference documentation",
		Provider:    ProviderNative,
		DefaultAlt:  "Go Reference",
		Resolver:    &goReferenceResolver{},
	})

	Register(Definition{
		ID:          "go-version",
		Format:      "badge",
		Category:    "github",
		Description: "Go version used in repository",
		Provider:    ProviderShields,
		DefaultAlt:  "Go Version",
		Resolver: ShieldsResolver{
			PathTemplate: "github/go-mod/go-version/{repo}",
			LinkTemplate: "https://github.com/{repo}",
			DefaultLogo:  "go",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})
}

type goReportCardResolver struct{}

func (r *goReportCardResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	module := params["module"]
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://goreportcard.com/badge/%s", module),
		LinkURL:  fmt.Sprintf("https://goreportcard.com/report/%s", module),
	}, nil
}

func (r *goReportCardResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "module", Required: true, Help: "Full Go module path (e.g. github.com/sofmeright/stagefreight)"},
		},
		Example: map[string]string{"module": "github.com/sofmeright/stagefreight"},
	}
}

type goReferenceResolver struct{}

func (r *goReferenceResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	module := params["module"]
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://pkg.go.dev/badge/%s.svg", module),
		LinkURL:  fmt.Sprintf("https://pkg.go.dev/%s", module),
	}, nil
}

func (r *goReferenceResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "module", Required: true, Help: "Full Go module path (e.g. github.com/sofmeright/stagefreight)"},
		},
		Example: map[string]string{"module": "github.com/sofmeright/stagefreight"},
	}
}
