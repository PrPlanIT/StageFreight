package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// A repository that releases several components lists them interleaved; its latest
// release can be another component's.
func monorepoReleases(t *testing.T) *httptest.Server {
	t.Helper()
	old := stamp(10 * day)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			fmt.Fprintf(w, `{"tag_name":"kyaml/v0.21.3","published_at":%q}`, old)
			return
		}
		fmt.Fprintf(w, `[{"tag_name":"kyaml/v0.21.3","published_at":%[1]q},{"tag_name":"api/v0.21.3","published_at":%[1]q},`+
			`{"tag_name":"kustomize/v5.8.3","published_at":%[1]q},{"tag_name":"kyaml/v0.21.2","published_at":%[1]q},`+
			`{"tag_name":"kustomize/v5.8.2","published_at":%[1]q}]`, old)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestToolchains_ReleaseTagPrefix(t *testing.T) {
	for constraint, want := range map[string]struct{ current, latest string }{
		"5.5.0": {"5.5.0", "5.8.3"}, // exact: newest kustomize release, not the repo's latest
		"x":     {"5.8.3", "5.8.3"},
		"0.x":   {"", ""}, // kyaml/api releases are not kustomize versions
	} {
		m := cooldownResolver(t, "github", monorepoReleases(t).URL)
		m.SetToolchainDesired(map[string]config.ToolConstraint{"kustomize": {Constraint: constraint}})
		deps := m.checkToolchains(context.Background(), m.desired)
		if len(deps) != 1 || deps[0].Current != want.current || deps[0].Latest != want.latest {
			t.Errorf("%s: got %+v, want current %q latest %q", constraint, deps, want.current, want.latest)
		}
	}
}

func TestTagPrefix(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv${KUSTOMIZE_VERSION}/kustomize_v${KUSTOMIZE_VERSION}_linux_amd64.tar.gz": "kustomize/",
		"https://github.com/o/r/releases/download/cmd%2Fbuilder%2Fv${V}/builder":                                                                              "cmd/builder/",
		"https://github.com/aquasecurity/trivy/releases/download/v${TRIVY_VERSION}/trivy.tar.gz":                                                              "",
		"https://example.com/trivy.tar.gz": "",
		"":                                 "",
	} {
		if got := tagPrefix(url); got != want {
			t.Errorf("tagPrefix(%q) = %q, want %q", url, got, want)
		}
	}
}
