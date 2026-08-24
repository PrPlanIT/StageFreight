package discovery

import "testing"

// versionFromTag must yield the bare version even for projects whose GitHub tags
// carry a name prefix (kustomize tags as "kustomize/v5.8.1"). Regression for the
// deps-engine bug that wrote KUSTOMIZE_VERSION=vkustomize/v5.8.1 → 404 build.
func TestVersionFromTag(t *testing.T) {
	cases := map[string]string{
		"kustomize/v5.8.1": "5.8.1", // the bug: prefixed tag
		"v4.53.6":          "4.53.6", // yq — normal tag
		"v3.13.3":          "3.13.3", // sops
		"5.8.1":            "5.8.1",  // already bare
		"kustomize/v5.8.1-rc1": "5.8.1-rc1",
		"api/v1.2.3":       "1.2.3", // any monorepo-style prefix
		"":                 "",
	}
	for tag, want := range cases {
		if got := versionFromTag(tag); got != want {
			t.Errorf("versionFromTag(%q) = %q, want %q", tag, got, want)
		}
	}
}
