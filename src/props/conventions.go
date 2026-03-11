package props

func init() {
	Register(Definition{
		ID:          "conventional-commits",
		Format:      "badge",
		Category:    "conventions",
		Description: "Conventional Commits specification badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "Conventional Commits",
		Resolver: StaticResolver{
			ImageURL: "https://img.shields.io/badge/Conventional%20Commits-1.0.0-%23FE5196?logo=conventionalcommits&logoColor=white",
			LinkURL:  "https://conventionalcommits.org",
			Params:   []ParamSpec{},
			Example:  map[string]string{},
		},
	})

	Register(Definition{
		ID:          "semantic-release",
		Format:      "badge",
		Category:    "conventions",
		Description: "Semantic Release badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "Semantic Release",
		Resolver: StaticResolver{
			ImageURL: "https://img.shields.io/badge/%20%20%F0%9F%93%A6%F0%9F%9A%80-semantic--release-e10079.svg",
			LinkURL:  "https://github.com/semantic-release/semantic-release",
			Params:   []ParamSpec{},
			Example:  map[string]string{},
		},
	})
}
