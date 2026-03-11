package props

func init() {
	Register(Definition{
		ID:          "docker-pulls",
		Format:      "badge",
		Category:    "docker",
		Description: "Docker Hub pull count",
		Provider:    ProviderShields,
		DefaultAlt:  "Docker Pulls",
		Resolver: ShieldsResolver{
			PathTemplate: "docker/pulls/{image}",
			LinkTemplate: "https://hub.docker.com/r/{image}",
			DefaultLogo:  "docker",
			Params: []ParamSpec{
				{Name: "image", Required: true, Help: "Docker Hub image (org/name)"},
			},
			Example: map[string]string{"image": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "docker-stars",
		Format:      "badge",
		Category:    "docker",
		Description: "Docker Hub star count",
		Provider:    ProviderShields,
		DefaultAlt:  "Docker Stars",
		Resolver: ShieldsResolver{
			PathTemplate: "docker/stars/{image}",
			LinkTemplate: "https://hub.docker.com/r/{image}",
			DefaultLogo:  "docker",
			Params: []ParamSpec{
				{Name: "image", Required: true, Help: "Docker Hub image (org/name)"},
			},
			Example: map[string]string{"image": "prplanit/stagefreight"},
		},
	})

	Register(Definition{
		ID:          "docker-image-size",
		Format:      "badge",
		Category:    "docker",
		Description: "Docker image size",
		Provider:    ProviderShields,
		DefaultAlt:  "Docker Image Size",
		Resolver: ShieldsResolver{
			PathTemplate: "docker/image-size/{image}/{tag}",
			LinkTemplate: "https://hub.docker.com/r/{image}",
			DefaultLogo:  "docker",
			Params: []ParamSpec{
				{Name: "image", Required: true, Help: "Docker Hub image (org/name)"},
				{Name: "tag", Default: "latest", Help: "Image tag (default: latest)"},
			},
			Example: map[string]string{"image": "prplanit/stagefreight", "tag": "latest"},
		},
	})

	Register(Definition{
		ID:          "docker-version",
		Format:      "badge",
		Category:    "docker",
		Description: "Docker image latest version",
		Provider:    ProviderShields,
		DefaultAlt:  "Docker Version",
		Resolver: ShieldsResolver{
			PathTemplate: "docker/v/{image}",
			LinkTemplate: "https://hub.docker.com/r/{image}",
			DefaultLogo:  "docker",
			Params: []ParamSpec{
				{Name: "image", Required: true, Help: "Docker Hub image (org/name)"},
			},
			Example: map[string]string{"image": "prplanit/stagefreight"},
		},
	})
}
