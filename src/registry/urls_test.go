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
			"ghcr", "github", "ghcr.io", "sofmeright/stagefreight", "1.0.0",
			"GitHub Container Registry",
			"https://github.com/sofmeright/packages/container/package/stagefreight",
			"https://github.com/sofmeright/packages/container/package/stagefreight",
			"ghcr.io/sofmeright/stagefreight",
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

func TestUnknownProviderPanics(t *testing.T) {
	rt := ResolvedRegistryTarget{Provider: "bogus", Host: "x.io", Path: "a/b"}

	assertPanics(t, "RepoURL", func() { rt.RepoURL() })
	assertPanics(t, "TagURL", func() { rt.TagURL("v1") })
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("%s did not panic on unknown provider", name)
		}
	}()
	fn()
}
