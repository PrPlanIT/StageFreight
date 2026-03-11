package props

import "fmt"

func init() {
	Register(Definition{
		ID:          "openssf-scorecard",
		Format:      "badge",
		Category:    "security",
		Description: "OpenSSF Scorecard security score",
		Provider:    ProviderNative,
		DefaultAlt:  "OpenSSF Scorecard",
		Resolver:    &openssfScorecardResolver{},
	})

	Register(Definition{
		ID:          "openssf-best-practices",
		Format:      "badge",
		Category:    "security",
		Description: "OpenSSF Best Practices (CII) badge",
		Provider:    ProviderNative,
		DefaultAlt:  "OpenSSF Best Practices",
		Resolver:    &openssfBestPracticesResolver{},
	})

	Register(Definition{
		ID:          "fossa-license",
		Format:      "badge",
		Category:    "security",
		Description: "FOSSA license compliance scan",
		Provider:    ProviderNative,
		DefaultAlt:  "FOSSA License",
		Resolver:    &fossaResolver{badgeType: "large"},
	})

	Register(Definition{
		ID:          "fossa-security",
		Format:      "badge",
		Category:    "security",
		Description: "FOSSA security scan",
		Provider:    ProviderNative,
		DefaultAlt:  "FOSSA Security",
		Resolver:    &fossaResolver{badgeType: "shield"},
	})

	Register(Definition{
		ID:          "slsa",
		Format:      "badge",
		Category:    "security",
		Description: "SLSA supply chain security level",
		Provider:    ProviderStatic,
		DefaultAlt:  "SLSA Level",
		Resolver: StaticResolver{
			ImageURL: "https://slsa.dev/images/gh-badge-level{level}.svg",
			LinkURL:  "https://slsa.dev",
			Params: []ParamSpec{
				{Name: "level", Required: true, Help: "SLSA level (1, 2, 3, or 4)"},
			},
			Example: map[string]string{"level": "3"},
		},
	})
}

type openssfScorecardResolver struct{}

func (r *openssfScorecardResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	repo := params["repo"]
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://api.securityscorecards.dev/projects/github.com/%s/badge", repo),
		LinkURL:  fmt.Sprintf("https://securityscorecards.dev/viewer/?uri=github.com/%s", repo),
	}, nil
}

func (r *openssfScorecardResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "repo", Required: true, Help: "GitHub owner/name (e.g. prplanit/stagefreight)"},
		},
		Example: map[string]string{"repo": "prplanit/stagefreight"},
	}
}

type openssfBestPracticesResolver struct{}

func (r *openssfBestPracticesResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	projectID := params["project_id"]
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://www.bestpractices.dev/projects/%s/badge", projectID),
		LinkURL:  fmt.Sprintf("https://www.bestpractices.dev/projects/%s", projectID),
	}, nil
}

func (r *openssfBestPracticesResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "project_id", Required: true, Help: "CII/OpenSSF project ID (numeric)"},
		},
		Example: map[string]string{"project_id": "7397"},
	}
}

type fossaResolver struct {
	badgeType string // "large" (license) or "shield" (security)
}

func (r *fossaResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	project := params["project"]
	// FOSSA badge URL pattern: /api/projects/{project}.svg?type={type}
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://app.fossa.com/api/projects/%s.svg?type=%s", project, r.badgeType),
		LinkURL:  fmt.Sprintf("https://app.fossa.com/projects/%s", project),
	}, nil
}

func (r *fossaResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "project", Required: true, Help: "FOSSA project identifier"},
		},
		Example: map[string]string{"project": "git%2Bgithub.com%2Fprplanit%2Fstagefreight"},
	}
}
