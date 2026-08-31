package scribe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// A scribe-rendered shield badge whose message is {inventory.<cluster>.count} must resolve
// the count from the committed manifest — the same fact set the postbuild badge path uses.
//
// Regression guard: scribe once resolved shield-badge messages through ScribeRegistry
// (gitver-only, no inventory resolver), so {inventory.*} survived verbatim into the README
// while the postbuild badge path resolved it — the split that left dungeon's Applications
// badge rendering the literal "{inventory.dungeon.count}". Badge messages must resolve
// through BadgeRegistry; this test pins that so the two paths cannot drift apart again.
func TestRenderShield_InventoryCountResolvesFromManifest(t *testing.T) {
	dir := t.TempDir()

	// Committed manifest: 2 active + 1 missing (both counted) + 1 graveyard (excluded) → 3.
	manifest := `{
  "schema_version": 1,
  "cluster": "testcluster",
  "discovery_status": {"complete": true, "source": "live_cluster"},
  "apps": {
    "ns/a": {"lifecycle": {"state": "active"}},
    "ns/b": {"lifecycle": {"state": "active"}},
    "ns/c": {"lifecycle": {"state": "missing"}},
    "ns/d": {"lifecycle": {"state": "graveyard"}}
  }
}`
	mdir := filepath.Join(dir, ".stagefreight", "manifests")
	if err := os.MkdirAll(mdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mdir, "k8s-inventory-testcluster.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	yml := `version: 1
forges:
  gitlab: { provider: gitlab, url: "https://gitlab.prplanit.com", credentials: GITLAB, default_path: "{org}/{repo}" }
orgs:
  PrPlanIT: { maintainer: "PrPlanIT <x@y>", aliases: { handle: prplanit } }
metadata:
  org: PrPlanIT
repos:
  primary: { forge: gitlab, project: "PrPlanIT/MaintenancePolicy", roles: [primary], branches: { default: main } }
stencils:
  apps:
    render: shield
    label: Applications
    message: "{inventory.testcluster.count}"
    color: "#0F1689"
    link: "docs/Apps_&_Services-Overview.md"
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	appCfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got, err := resolveStencilMarkdown(appCfg, appCfg.StencilsByID()["apps"], "", "", &gitver.VersionInfo{}, dir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "inventory.testcluster.count") {
		t.Errorf("badge message left {inventory.testcluster.count} unresolved:\n%s", got)
	}
	if !strings.Contains(got, "Applications-3") {
		t.Errorf("badge did not render the manifest count (3):\n%s", got)
	}
}
