package discovery

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// A *_VERSION whose value is a branch ref (develop) must NOT be classified as a
// pinned, updatable tool. A real version value (1.2.3) must be.
func TestCrossRefTools_SkipsBranchRef(t *testing.T) {
	info := &supplychain.DockerFreshnessInfo{
		EnvVars: map[string]supplychain.EnvVar{
			"OSTICKET_PLUGINS_VERSION": {Name: "OSTICKET_PLUGINS_VERSION", Value: "develop", Line: 10},
			"BAR_VERSION":              {Name: "BAR_VERSION", Value: "1.2.3", Line: 11},
		},
	}
	tools := crossRefTools(info)

	byName := make(map[string]supplychain.PinnedTool, len(tools))
	for _, tl := range tools {
		byName[tl.EnvName] = tl
	}
	if _, ok := byName["OSTICKET_PLUGINS_VERSION"]; ok {
		t.Error("branch ref OSTICKET_PLUGINS_VERSION=develop was classified as a pinned tool; want skipped")
	}
	bar, ok := byName["BAR_VERSION"]
	if !ok {
		t.Fatal("BAR_VERSION=1.2.3 was not classified as a pinned tool; want kept")
	}
	if bar.Version != "1.2.3" {
		t.Errorf("BAR_VERSION value = %q, want 1.2.3", bar.Version)
	}
}
