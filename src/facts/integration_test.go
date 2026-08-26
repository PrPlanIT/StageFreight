package facts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestEndToEnd_IdentityFromLoadedConfig is the holistic Phase A proof: a real
// .stagefreight.yml with orgs/metadata/repos/forges/registries is decoded through the
// actual config.Load path (KnownFields + id-maps + validation), then resolved through
// the real BadgeRegistry — confirming the identity model works end to end, not just in
// hand-built unit configs.
func TestEndToEnd_IdentityFromLoadedConfig(t *testing.T) {
	dir := t.TempDir()
	yml := `version: 1
forges:
  gitlab: { provider: gitlab, url: "https://gl.example", default_path: "{org}/{repo}" }
registries:
  dockerhub: { provider: docker, url: "docker.io", default_path: "{org.handle}/{repo}" }
  ghcr: { provider: ghcr, url: "ghcr.io", default_path: "{org.lower}/{repo}" }
orgs:
  HomeLabHD:
    maintainer: "HomeLabHD <ops@prplanit.com>"
    aliases: { handle: hlhd }
metadata:
  org: HomeLabHD
  title: "ARK Server"
  license: MIT
  names: { dockerhub: arkserver }
repos:
  primary: { forge: gitlab, project: "HomeLabHD/ark-se-server", roles: [primary], branches: { default: main } }
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	got := BadgeRegistry().Resolve([]string{
		"{metadata.title} · {metadata.license} · {org.maintainer}",
		"{path.gitlab}",
		"{path.dockerhub}", // names override → arkserver
		"{path.ghcr}",      // no override → slug
		"{slug}",
	}, &Context{Config: cfg})

	want := []string{
		"ARK Server · MIT · HomeLabHD <ops@prplanit.com>",
		"HomeLabHD/ark-se-server",
		"hlhd/arkserver",
		"homelabhd/ark-se-server",
		"ark-se-server",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
