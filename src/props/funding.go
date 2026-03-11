package props

func init() {
	Register(Definition{
		ID:          "github-sponsors",
		Format:      "badge",
		Category:    "funding",
		Description: "GitHub Sponsors badge",
		Provider:    ProviderShields,
		DefaultAlt:  "GitHub Sponsors",
		Resolver: ShieldsResolver{
			PathTemplate: "github/sponsors/{user}",
			LinkTemplate: "https://github.com/sponsors/{user}",
			DefaultLogo:  "githubsponsors",
			Params: []ParamSpec{
				{Name: "user", Required: true, Help: "GitHub username"},
			},
			Example: map[string]string{"user": "prplanit"},
		},
	})

	Register(Definition{
		ID:          "paypal-donate",
		Format:      "badge",
		Category:    "funding",
		Description: "PayPal donation badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "PayPal Donate",
		Resolver: StaticResolver{
			ImageURL: "https://img.shields.io/badge/PayPal-donate-blue?logo=paypal",
			LinkURL:  "https://www.paypal.com/donate/?hosted_button_id={paypal_id}",
			Params: []ParamSpec{
				{Name: "paypal_id", Required: true, Help: "PayPal hosted button ID"},
			},
			Example: map[string]string{"paypal_id": "EXAMPLE123"},
		},
	})
}
