package props

import (
	"fmt"
	"net/url"
)

func init() {
	// ── Release & Version ────────────────────────────────────────────────

	Register(Definition{
		ID:          "github-release",
		Format:      "badge",
		Category:    "release",
		Description: "Latest GitHub release version",
		Provider:    ProviderShields,
		DefaultAlt:  "GitHub Release",
		Resolver: ShieldsResolver{
			PathTemplate: "github/v/release/{repo}",
			LinkTemplate: "https://github.com/{repo}/releases/latest",
			DefaultLogo:  "github",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name (e.g. prplanit/stagefreight)"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-tag",
		Format:      "badge",
		Category:    "release",
		Description: "Latest GitHub tag",
		Provider:    ProviderShields,
		DefaultAlt:  "GitHub Tag",
		Resolver: ShieldsResolver{
			PathTemplate: "github/v/tag/{repo}",
			LinkTemplate: "https://github.com/{repo}/tags",
			DefaultLogo:  "github",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-license",
		Format:      "badge",
		Category:    "release",
		Description: "Repository license",
		Provider:    ProviderShields,
		DefaultAlt:  "License",
		Resolver: ShieldsResolver{
			PathTemplate: "github/license/{repo}",
			LinkTemplate: "https://github.com/{repo}/blob/main/LICENSE",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-contributors",
		Format:      "badge",
		Category:    "release",
		Description: "Number of contributors",
		Provider:    ProviderShields,
		DefaultAlt:  "Contributors",
		Resolver: ShieldsResolver{
			PathTemplate: "github/contributors/{repo}",
			LinkTemplate: "https://github.com/{repo}/graphs/contributors",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	// ── GitHub Stats ─────────────────────────────────────────────────────

	Register(Definition{
		ID:          "github-issues-open",
		Format:      "badge",
		Category:    "github",
		Description: "Open issues count",
		Provider:    ProviderShields,
		DefaultAlt:  "Open Issues",
		Resolver: ShieldsResolver{
			PathTemplate: "github/issues/{repo}",
			LinkTemplate: "https://github.com/{repo}/issues",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-issues-closed",
		Format:      "badge",
		Category:    "github",
		Description: "Closed issues count",
		Provider:    ProviderShields,
		DefaultAlt:  "Closed Issues",
		Resolver: ShieldsResolver{
			PathTemplate: "github/issues-closed/{repo}",
			LinkTemplate: "https://github.com/{repo}/issues?q=is%3Aissue+is%3Aclosed",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-prs-closed",
		Format:      "badge",
		Category:    "github",
		Description: "Closed pull requests count",
		Provider:    ProviderShields,
		DefaultAlt:  "Closed PRs",
		Resolver: ShieldsResolver{
			PathTemplate: "github/issues-pr-closed/{repo}",
			LinkTemplate: "https://github.com/{repo}/pulls?q=is%3Apr+is%3Aclosed",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-last-commit",
		Format:      "badge",
		Category:    "github",
		Description: "Last commit date",
		Provider:    ProviderShields,
		DefaultAlt:  "Last Commit",
		Resolver: ShieldsResolver{
			PathTemplate: "github/last-commit/{repo}",
			LinkTemplate: "https://github.com/{repo}/commits",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "github-commit-activity",
		Format:      "badge",
		Category:    "github",
		Description: "Commit activity over time",
		Provider:    ProviderShields,
		DefaultAlt:  "Commit Activity",
		Resolver: ShieldsResolver{
			PathTemplate: "github/commit-activity/{interval}/{repo}",
			LinkTemplate: "https://github.com/{repo}/graphs/commit-activity",
			DefaultLogo:  "",
			Params: []ParamSpec{
				{Name: "repo", Required: true, Help: "GitHub owner/name"},
				{Name: "interval", Default: "m", Help: "Activity interval: y (year), m (month), w (week)"},
			},
			Example: map[string]string{"repo": "prplanit/stagefreight", "interval": "m"},
		},
	})

	// ── Artifact Hub ─────────────────────────────────────────────────────

	Register(Definition{
		ID:          "artifact-hub",
		Format:      "badge",
		Category:    "release",
		Description: "Artifact Hub package badge",
		Provider:    ProviderNative,
		DefaultAlt:  "Artifact Hub",
		Resolver:    &artifactHubResolver{},
	})
}

// artifactHubResolver constructs Artifact Hub badge URLs.
type artifactHubResolver struct{}

func (r *artifactHubResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	pkgType := params["package_type"]
	org := params["org"]
	name := params["name"]

	// The endpoint URL must be URL-encoded as a query parameter value.
	endpoint := fmt.Sprintf("https://artifacthub.io/badge/repository/%s", name)
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://img.shields.io/endpoint?url=%s", url.QueryEscape(endpoint)),
		LinkURL:  fmt.Sprintf("https://artifacthub.io/packages/%s/%s/%s", pkgType, org, name),
	}, nil
}

func (r *artifactHubResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "package_type", Required: true, Help: "Package type (e.g. helm, falco, opa)"},
			{Name: "org", Required: true, Help: "Organization name on Artifact Hub"},
			{Name: "name", Required: true, Help: "Package name"},
		},
		Example: map[string]string{
			"package_type": "helm",
			"org":          "prplanit",
			"name":         "stagefreight",
		},
	}
}
