package props

import (
	"fmt"
	"net/url"
)

func init() {
	Register(Definition{
		ID:          "slack",
		Format:      "badge",
		Category:    "social",
		Description: "Slack workspace invite badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "Slack",
		Resolver:    &slackResolver{},
	})

	Register(Definition{
		ID:          "discord",
		Format:      "badge",
		Category:    "social",
		Description: "Discord server member count",
		Provider:    ProviderShields,
		DefaultAlt:  "Discord",
		Resolver: ShieldsResolver{
			PathTemplate: "discord/{server_id}",
			LinkTemplate: "https://discord.gg/{server_id}",
			DefaultLogo:  "discord",
			Params: []ParamSpec{
				{Name: "server_id", Required: true, Help: "Discord server ID"},
			},
			Example: map[string]string{"server_id": "927474461786681344"},
		},
	})

	Register(Definition{
		ID:          "twitter",
		Format:      "badge",
		Category:    "social",
		Description: "Twitter/X follow badge",
		Provider:    ProviderShields,
		DefaultAlt:  "Twitter",
		Resolver: ShieldsResolver{
			PathTemplate: "twitter/follow/{handle}",
			LinkTemplate: "https://twitter.com/{handle}",
			DefaultLogo:  "x",
			Params: []ParamSpec{
				{Name: "handle", Required: true, Help: "Twitter/X handle (without @)"},
			},
			Example: map[string]string{"handle": "prplanit"},
		},
	})

	Register(Definition{
		ID:          "bluesky",
		Format:      "badge",
		Category:    "social",
		Description: "Bluesky profile badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "Bluesky",
		Resolver: StaticResolver{
			ImageURL: "https://img.shields.io/badge/Bluesky-follow-blue?logo=bluesky",
			LinkURL:  "https://bsky.app/profile/{handle}",
			Params: []ParamSpec{
				{Name: "handle", Required: true, Help: "Bluesky handle (e.g. user.bsky.social)"},
			},
			Example: map[string]string{"handle": "prplanit.bsky.social"},
		},
	})

	Register(Definition{
		ID:          "linkedin",
		Format:      "badge",
		Category:    "social",
		Description: "LinkedIn company page badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "LinkedIn",
		Resolver: StaticResolver{
			ImageURL: "https://img.shields.io/badge/LinkedIn-connect-blue?logo=linkedin",
			LinkURL:  "https://www.linkedin.com/company/{company}",
			Params: []ParamSpec{
				{Name: "company", Required: true, Help: "LinkedIn company slug"},
			},
			Example: map[string]string{"company": "precisionplanit"},
		},
	})

	Register(Definition{
		ID:          "website",
		Format:      "badge",
		Category:    "social",
		Description: "Project website link badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "Website",
		Resolver:    &websiteResolver{},
	})

	Register(Definition{
		ID:          "contact",
		Format:      "badge",
		Category:    "social",
		Description: "Contact information badge",
		Provider:    ProviderStatic,
		DefaultAlt:  "Contact",
		Resolver:    &contactResolver{},
	})
}

// slackResolver builds a Slack invite badge with an encoded label.
type slackResolver struct{}

func (r *slackResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	workspaceURL := params["workspace_url"]
	return ResolvedProp{
		ImageURL: "https://img.shields.io/badge/slack-join-brightgreen?logo=slack",
		LinkURL:  workspaceURL,
	}, nil
}

func (r *slackResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "workspace_url", Required: true, Help: "Slack workspace invite URL"},
		},
		Example: map[string]string{
			"workspace_url": "https://slack.example.com",
		},
	}
}

type websiteResolver struct{}

func (r *websiteResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	siteURL := params["url"]
	label := "Website"
	if l, ok := params["label"]; ok && l != "" {
		label = l
	}
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://img.shields.io/badge/%s-visit-blue", shieldsBadgeEscape(label)),
		LinkURL:  siteURL,
	}, nil
}

func (r *websiteResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "url", Required: true, Help: "Website URL"},
			{Name: "label", Default: "Website", Help: "Badge label text"},
		},
		Example: map[string]string{"url": "https://prplanit.com", "label": "Docs"},
	}
}

type contactResolver struct{}

func (r *contactResolver) Resolve(params map[string]string, opts RenderOptions) (ResolvedProp, error) {
	label := params["label"]
	linkURL := ""
	if u, ok := params["url"]; ok {
		linkURL = u
	}
	return ResolvedProp{
		ImageURL: fmt.Sprintf("https://img.shields.io/badge/contact-%s-blue", shieldsBadgeEscape(label)),
		LinkURL:  linkURL,
	}, nil
}

func (r *contactResolver) Schema() PropSchema {
	return PropSchema{
		Params: []ParamSpec{
			{Name: "label", Required: true, Help: "Contact label (e.g. email, support)"},
			{Name: "url", Help: "Contact URL"},
		},
		Example: map[string]string{"label": "support", "url": "mailto:support@prplanit.com"},
	}
}

// shieldsBadgeEscape encodes a string for use in a shields.io static badge URL path segment.
// Shields.io interprets dashes as separators and underscores as spaces, so these must be escaped.
// Uses URL percent-encoding for special characters.
func shieldsBadgeEscape(s string) string {
	return url.PathEscape(s)
}
