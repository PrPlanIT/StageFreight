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

// Every allow_failure producer uploads its artifacts even on failure. An allow_failure
// job is one the pipeline is designed to run PAST — so a downstream consumer that runs
// anyway (narrate always; publish's authorization gate reads review's ledger fragment)
// will need that artifact precisely when the producer failed. Without when:always the
// producer's failure both continues the pipeline AND drops the artifact, so the consumer
// dies on "artifact not found" instead of acting on the recorded outcome. review (security
// evidence) and publish (shipped facts) are the cases; the invariant is forge-neutral —
// the GitHub/Gitea/Forgejo, GitLab, and Azure emitters all render Artifacts.WhenAlways.
func TestPlan_AllowFailureProducersUploadOnFailure(t *testing.T) {
	cfg := &config.Config{}
	cfg.CI.Image = "example/stagefreight:latest"
	p, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range p.Jobs {
		if j.Policy.AllowFailure && len(j.Artifacts.Paths) > 0 && !j.Artifacts.WhenAlways {
			t.Errorf("%s is allow_failure and produces %v but does not upload on failure (WhenAlways=false) — a downstream consumer will break on the missing artifact exactly when %s fails",
				j.Name, j.Artifacts.Paths, j.Name)
		}
	}
}

// narrate states the run's verdict: it always runs, and its failure is the pipeline's, never
// tolerated, so a failed release can not end in a green pipeline.
func TestPlan_NarrateCarriesTheVerdict(t *testing.T) {
	cfg := &config.Config{}
	cfg.CI.Image = "example/stagefreight:latest"
	p, err := Plan(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range p.Jobs {
		if j.Name != "narrate" {
			continue
		}
		if j.Policy.AllowFailure || !j.Policy.WhenAlways {
			t.Fatalf("narrate policy = %+v, want it to always run and never be allowed to fail", j.Policy)
		}
		return
	}
	t.Fatal("no narrate job planned")
}
