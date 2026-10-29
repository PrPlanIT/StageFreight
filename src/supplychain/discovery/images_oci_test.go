package discovery

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestRepoPath(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/element-hq/synapse": "element-hq/synapse", // strip the host
		"element-hq/synapse":         "element-hq/synapse", // no host, pass through
		"library/nginx":              "library/nginx",
		"nginx":                      "nginx",
		"registry:5000/team/app":     "team/app", // host carries a port
	} {
		if got := repoPath(in); got != want {
			t.Errorf("repoPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImageRegistryResolution(t *testing.T) {
	regs := []config.RegistryConfig{{ID: "ghcr-proxy", Provider: "ghcr", URL: "https://ghcr.cr.pcfae.com"}}
	if (&Resolver{}).imageRegistry() != nil {
		t.Error("no dependency.registry ref should resolve to nil (Docker Hub fallback)")
	}
	if r := (&Resolver{registries: regs, imageRegistryRef: "ghcr-proxy"}).imageRegistry(); r == nil || r.Provider != "ghcr" {
		t.Errorf("a ref should resolve its registry from the single source of truth, got %v", r)
	}
	if (&Resolver{registries: regs, imageRegistryRef: "nope"}).imageRegistry() != nil {
		t.Error("an unknown ref should resolve to nil, not panic")
	}
}

// TestOCITagsListsTagsOverDistributionAPI runs against a real in-memory OCI registry, proving
// discovery reads the distribution-API {name, tags} shape — the exact shape the old
// Docker-Hub-only parser ({results, next}) silently dropped, yielding zero tags for ghcr/Harbor.
func TestOCITagsListsTagsOverDistributionAPI(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	for _, v := range []string{"1.162.0", "1.164.1"} {
		tag, err := name.NewTag(host+"/element-hq/synapse:"+v, name.WeakValidation)
		if err != nil {
			t.Fatal(err)
		}
		if err := remote.Write(tag, empty.Image); err != nil {
			t.Fatal(err)
		}
	}

	m := &Resolver{}
	reg := &config.RegistryConfig{ID: "ghcr-proxy", Provider: "ghcr", URL: srv.URL}
	// The image still carries its ghcr.io host; repoPath strips it so the lookup lands on the
	// proxy's element-hq/synapse — this is the ghcr→Harbor routing in miniature.
	tags, src, err := m.ociTags(context.Background(), reg, "ghcr.io/element-hq/synapse")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tg := range tags {
		got[tg] = true
	}
	if !got["1.162.0"] || !got["1.164.1"] {
		t.Fatalf("expected both tags over the OCI API, got %v", tags)
	}
	if !strings.Contains(src, "element-hq/synapse/tags/list") {
		t.Errorf("source url should name the tags/list endpoint, got %q", src)
	}
}
