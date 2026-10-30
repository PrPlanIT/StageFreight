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

func TestIsDockerHub(t *testing.T) {
	for in, want := range map[string]bool{
		"golang":                        true, // no host → Docker Hub (library/golang)
		"library/nginx":                 true,
		"prplanit/stagefreight":         true,
		"docker.io/library/nginx":       true,
		"index.docker.io/library/nginx": true,
		"ghcr.io/element-hq/synapse":    false, // carries its own canonical host
		"quay.io/prometheus/node":       false,
		"lscr.io/linuxserver/radarr":    false,
		"registry:5000/team/app":        false, // host with a port
	} {
		if got := isDockerHub(in); got != want {
			t.Errorf("isDockerHub(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestOCITagsCanonicalListsFromOwnHost proves a host-qualified image (ghcr.io/…) is enumerated
// over its OWN registry's distribution API — the default when no dependency.registry override is
// set, and the path that fixes a base a pull-through proxy could never enumerate.
func TestOCITagsCanonicalListsFromOwnHost(t *testing.T) {
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

	tags, src, err := ociTagsCanonical(context.Background(), host+"/element-hq/synapse")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tg := range tags {
		got[tg] = true
	}
	if !got["1.162.0"] || !got["1.164.1"] {
		t.Fatalf("expected both tags over the image's own OCI API, got %v", tags)
	}
	if !strings.Contains(src, "element-hq/synapse/tags/list") {
		t.Errorf("source url should name the tags/list endpoint, got %q", src)
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
