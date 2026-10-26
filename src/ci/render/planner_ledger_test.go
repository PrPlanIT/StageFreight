package render

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// Every job that carries the state root uploads it even when it fails: narrate runs
// always and reads cistate through its needs, so a blocked perform that dropped its
// artifact left narrate with "missing cistate" on exactly the pipelines it must report.
func TestPlan_StateRootCarriersUploadOnFailure(t *testing.T) {
	cfg := &config.Config{}
	cfg.CI.Image = "example/stagefreight:latest"
	p, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range p.Jobs {
		for _, path := range j.Artifacts.Paths {
			if path == paths.Root+"/" && !j.Artifacts.WhenAlways {
				t.Errorf("%s carries %s but does not upload it on failure", j.Name, path)
			}
		}
	}
}
