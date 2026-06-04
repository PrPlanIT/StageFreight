package registry

import "testing"

func TestResolvedRegistryTargetURLs(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		host     string
		path     string
		tag      string
		wantName string
		wantRepo string
		wantTag  string
		wantRef  string
	}{
		{
			"docker", "docker", "docker.io", "prplanit/stagefreight", "1.0.0",
			"Docker Hub",
			"https://hub.docker.com/r/prplanit/stagefreight",
			"https://hub.docker.com/r/prplanit/stagefreight/tags?name=1.0.0",
			"docker.io/prplanit/stagefreight",
		},
		{
			"ghcr", "github", "ghcr.io", "prplanit/stagefreight", "1.0.0",
			"GitHub Container Registry",
			"https://github.com/prplanit/packages/container/package/stagefreight",
			"https://github.com/prplanit/packages/container/package/stagefreight",
			"ghcr.io/prplanit/stagefreight",
		},
		{
			"quay", "quay", "quay.io", "prplanit/stagefreight", "1.0.0",
			"Quay.io",
			"https://quay.io/repository/prplanit/stagefreight",
			"https://quay.io/repository/prplanit/stagefreight?tab=tags&tag=1.0.0",
			"quay.io/prplanit/stagefreight",
		},
		{
			"gitlab-selfhosted", "gitlab", "gitlab.example.com", "group/project/image", "1.0.0",
			"GitLab Registry",
			"https://gitlab.example.com/group/project/image/container_registry",
			"https://gitlab.example.com/group/project/image/container_registry",
			"gitlab.example.com/group/project/image",
		},
		{
			"gitlab-saas", "gitlab", "registry.gitlab.com", "myorg/myproject", "2.0.0",
			"GitLab Registry",
			"https://gitlab.com/myorg/myproject/container_registry",
			"https://gitlab.com/myorg/myproject/container_registry",
			"registry.gitlab.com/myorg/myproject",
		},
		{
			"harbor", "harbor", "harbor.example.com", "library/stagefreight", "1.0.0",
			"Harbor",
			"https://harbor.example.com/harbor/projects/library/repositories/stagefreight",
			"https://harbor.example.com/harbor/projects/library/repositories/stagefreight/artifacts-tab",
			"harbor.example.com/library/stagefreight",
		},
		{
			"gitea", "gitea", "git.example.com", "prplanit/stagefreight", "1.0.0",
			"Gitea Registry",
			"https://git.example.com/prplanit/-/packages/container/stagefreight",
			"https://git.example.com/prplanit/-/packages/container/stagefreight",
			"git.example.com/prplanit/stagefreight",
		},
		{
			"forgejo", "forgejo", "forgejo.example.com", "prplanit/stagefreight", "1.0.0",
			"Forgejo Registry",
			"https://forgejo.example.com/prplanit/-/packages/container/stagefreight",
			"https://forgejo.example.com/prplanit/-/packages/container/stagefreight",
			"forgejo.example.com/prplanit/stagefreight",
		},
		{
			"jfrog", "jfrog", "artifacts.example.com", "docker-local/stagefreight", "1.0.0",
			"JFrog Artifactory",
			"https://artifacts.example.com/ui/repos/tree/General/docker-local/stagefreight",
			"https://artifacts.example.com/ui/repos/tree/General/docker-local/stagefreight",
			"artifacts.example.com/docker-local/stagefreight",
		},
		{
			"generic", "generic", "registry.example.com", "myorg/myimage", "latest",
			"registry.example.com",
			"https://registry.example.com/myorg/myimage",
			"https://registry.example.com/myorg/myimage",
			"registry.example.com/myorg/myimage",
		},
		{
			"ecr", "ecr", "123456789.dkr.ecr.us-east-1.amazonaws.com", "myapp", "1.0.0",
			"Amazon ECR",
			"https://us-east-1.console.aws.amazon.com/ecr/repositories/private/123456789/myapp",
			"https://us-east-1.console.aws.amazon.com/ecr/repositories/private/123456789/myapp/_/image/1.0.0/details",
			"123456789.dkr.ecr.us-east-1.amazonaws.com/myapp",
		},
		{
			"gar", "gar", "us-docker.pkg.dev", "my-project/my-repo/my-image", "1.0.0",
			"Google Artifact Registry",
			"https://console.cloud.google.com/artifacts/docker/my-project/us/my-repo/my-image",
			"https://console.cloud.google.com/artifacts/docker/my-project/us/my-repo/my-image",
			"us-docker.pkg.dev/my-project/my-repo/my-image",
		},
		{
			"gar-subregion", "gar", "europe-west1-docker.pkg.dev", "proj/repo/img", "2.0.0",
			"Google Artifact Registry",
			"https://console.cloud.google.com/artifacts/docker/proj/europe-west1/repo/img",
			"https://console.cloud.google.com/artifacts/docker/proj/europe-west1/repo/img",
			"europe-west1-docker.pkg.dev/proj/repo/img",
		},
		{
			"acr", "acr", "myregistry.azurecr.io", "myorg/myapp", "1.0.0",
			"Azure Container Registry",
			"https://myregistry.azurecr.io/myorg/myapp",
			"https://myregistry.azurecr.io/myorg/myapp",
			"myregistry.azurecr.io/myorg/myapp",
		},
		{
			"nexus", "nexus", "nexus.example.com", "docker-hosted/myapp", "1.0.0",
			"Sonatype Nexus",
			"https://nexus.example.com/#browse/search/docker==docker-hosted/myapp",
			"https://nexus.example.com/#browse/search/docker==docker-hosted/myapp",
			"nexus.example.com/docker-hosted/myapp",
		},
		{
			"ecr-malformed", "ecr", "weird-ecr-host.example.com", "myapp", "1.0.0",
			"Amazon ECR",
			"https://weird-ecr-host.example.com/myapp",
			"https://weird-ecr-host.example.com/myapp",
			"weird-ecr-host.example.com/myapp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := ResolvedRegistryTarget{Provider: tc.provider, Host: tc.host, Path: tc.path}
			if got := rt.DisplayName(); got != tc.wantName {
				t.Fatalf("DisplayName=%q want %q", got, tc.wantName)
			}
			if got := rt.ImageRef(); got != tc.wantRef {
				t.Fatalf("ImageRef=%q want %q", got, tc.wantRef)
			}
			if got := rt.RepoURL(); got != tc.wantRepo {
				t.Fatalf("RepoURL=%q want %q", got, tc.wantRepo)
			}
			if got := rt.TagURL(tc.tag); got != tc.wantTag {
				t.Fatalf("TagURL=%q want %q", got, tc.wantTag)
			}
		})
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"docker.io", "docker.io"},
		{"https://docker.io", "docker.io"},
		{"http://docker.io", "docker.io"},
		{"https://ghcr.io/", "ghcr.io"},
		{"https://registry.gitlab.com/", "registry.gitlab.com"},
		{"harbor.example.com/", "harbor.example.com"},
	}
	for _, tc := range cases {
		if got := NormalizeHost(tc.input); got != tc.want {
			t.Fatalf("NormalizeHost(%q)=%q want %q", tc.input, got, tc.want)
		}
	}
}

func TestImageRefWithUnnormalizedHost(t *testing.T) {
	// Callers must use NormalizeHost before constructing the target.
	// This test verifies that NormalizeHost + ImageRef produces the right result.
	rt := ResolvedRegistryTarget{
		Provider: "docker",
		Host:     NormalizeHost("https://docker.io/"),
		Path:     "prplanit/stagefreight",
	}
	if got := rt.ImageRef(); got != "docker.io/prplanit/stagefreight" {
		t.Fatalf("ImageRef=%q", got)
	}
}

func TestUnknownProviderDegradesNotPanic(t *testing.T) {
	// A release-notes URL is presentation, not integrity: an unrecognized
	// provider must degrade to a generic URL, never panic and fail a completed,
	// verified publication. (Regression: providerFromHost once emitted "dockerhub"
	// — outside the canonical set — and crashed the v0.6.0 release job.)
	rt := ResolvedRegistryTarget{Provider: "bogus", Host: "x.io", Path: "a/b"}
	const wantGeneric = "https://x.io/a/b"
	if got := rt.RepoURL(); got != wantGeneric {
		t.Errorf("RepoURL on unknown provider = %q, want generic %q (must not panic)", got, wantGeneric)
	}
	if got := rt.TagURL("v1"); got != wantGeneric {
		t.Errorf("TagURL on unknown provider = %q, want generic %q (must not panic)", got, wantGeneric)
	}
	if got := rt.DisplayName(); got != "x.io" {
		t.Errorf("DisplayName on unknown provider = %q, want host fallback", got)
	}
}
