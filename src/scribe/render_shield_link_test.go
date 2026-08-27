package scribe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// A shield's link: field resolves fact families ({path.<surface>}, {metadata.*}) exactly
// like its badge cousins — earlier it ran only the vars pass, so {path.gitlab} in a link
// survived verbatim into the README. This renders a shield whose link uses {path.gitlab}
// (derived from the forge default_path + the repo's org/slug) and asserts it resolves.
func TestRenderShield_LinkResolvesPathFact(t *testing.T) {
	dir := t.TempDir()
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
  gl:
    render: shield
    label: GitLab
    message: source
    color: "#FC6D26"
    link: "https://gitlab.prplanit.com/{path.gitlab}"
`
	path := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	appCfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got, err := resolveStencilMarkdown(appCfg, appCfg.StencilsByID()["gl"], "", "", &gitver.VersionInfo{}, dir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(got, "{path.gitlab}") {
		t.Errorf("shield link left {path.gitlab} unresolved:\n%s", got)
	}
	if !strings.Contains(got, "gitlab.prplanit.com/PrPlanIT/MaintenancePolicy") {
		t.Errorf("shield link did not resolve to the derived path:\n%s", got)
	}
}
