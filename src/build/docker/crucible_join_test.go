package docker

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestCrucibleArtifactIDJoin verifies the load-bearing invariant introduced
// in phase 4C.2: every ArtifactID Crucible records against — built inline as
// NewArtifactID("docker", step.Name) from the StepResult — must resolve to
// an artifact in the OutputsManifest emitted by PlanToOutputs on the same
// publishPlan. If this join were broken, ResultsBuilder.Build would return
// an "unknown artifact ids" error at publish time; this test pins the
// successful-join shape so a regression in either side surfaces immediately.
func TestCrucibleArtifactIDJoin(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{
			{
				Name:       "api",
				Dockerfile: "api/Dockerfile",
				Context:    "api",
				Output:     build.OutputImage,
				Platforms:  []string{"linux/amd64"},
				Push:       true,
				Registries: []build.RegistryTarget{{
					URL: "docker.io", Path: "org/api", Tags: []string{"v1"},
				}},
			},
			{
				Name:       "worker",
				Dockerfile: "worker/Dockerfile",
				Context:    "worker",
				Output:     build.OutputImage,
				Platforms:  []string{"linux/amd64"},
				Push:       true,
				Registries: []build.RegistryTarget{{
					URL: "docker.io", Path: "org/worker", Tags: []string{"v1"},
				}},
			},
		},
	}

	outputs, err := build.PlanToOutputs(plan, build.PlanToOutputsOpts{
		GeneratedAt: "2026-05-31T00:00:00Z",
		Commit:      "abc123",
	})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}

	// Mirror Crucible's recording path: iterate pubResult.Steps where
	// StepResult.Name equals BuildStep.Name (buildx.go:103), build the
	// ArtifactID exactly as crucible.go does, record one push outcome per
	// observation.
	pubSteps := []build.StepResult{
		{
			Name: "api",
			Publications: []build.PushObservation{
				{Host: "docker.io", Path: "org/api", Tag: "v1", Digest: "sha256:aaa"},
			},
		},
		{
			Name: "worker",
			Publications: []build.PushObservation{
				{Host: "docker.io", Path: "org/worker", Tag: "v1", Digest: "sha256:bbb"},
			},
		},
	}

	rb := build.NewResultsBuilder()
	for _, step := range pubSteps {
		id := artifact.NewArtifactID("docker", step.Name)
		for _, obs := range step.Publications {
			rb.Record(id, artifact.Outcome{
				Type: artifact.OutcomeTypePush,
				Target: &artifact.OutcomeTarget{
					Kind: "registry", Host: obs.Host, Path: obs.Path, Tag: obs.Tag,
				},
				Push: &artifact.PushOutcome{
					Status: artifact.OutcomeSuccess, Digest: obs.Digest,
					ObservedDigest: obs.Digest, ObservedBy: "buildx",
				},
			})
		}
	}

	results, err := rb.Build(&outputs)
	if err != nil {
		t.Fatalf("ResultsBuilder.Build returned join error: %v", err)
	}

	// Every recorded Result must resolve to a known artifact in outputs.
	known := make(map[artifact.ArtifactID]artifact.Artifact, len(outputs.Artifacts))
	for _, a := range outputs.Artifacts {
		known[a.ID] = a
	}
	if len(results.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results.Results))
	}
	for _, r := range results.Results {
		a, ok := known[r.ArtifactID]
		if !ok {
			t.Fatalf("recorded ArtifactID %q not present in outputs.Artifacts", r.ArtifactID)
		}
		if r.ArtifactName != a.Name || r.Kind != a.Kind {
			t.Fatalf("Build did not propagate name/kind from outputs for %q: got (%q,%q) want (%q,%q)",
				r.ArtifactID, r.ArtifactName, r.Kind, a.Name, a.Kind)
		}
	}
}

// TestCrucibleArtifactIDJoin_RejectsUnknownStep proves the failure mode:
// if a StepResult escapes filtering and lands in pubResult.Steps without a
// matching outputs entry (e.g., a non-image step that PlanToOutputs skipped),
// Build returns an unknown-id error rather than silently emitting an
// orphan result.
func TestCrucibleArtifactIDJoin_RejectsUnknownStep(t *testing.T) {
	plan := &build.BuildPlan{
		Steps: []build.BuildStep{{
			Name:       "api",
			Dockerfile: "api/Dockerfile",
			Context:    "api",
			Output:     build.OutputImage,
			Platforms:  []string{"linux/amd64"},
			Push:       true,
			Registries: []build.RegistryTarget{{
				URL: "docker.io", Path: "org/api", Tags: []string{"v1"},
			}},
		}},
	}
	outputs, err := build.PlanToOutputs(plan, build.PlanToOutputsOpts{
		GeneratedAt: "2026-05-31T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("PlanToOutputs: %v", err)
	}

	rb := build.NewResultsBuilder()
	rb.Record(artifact.NewArtifactID("docker", "ghost"), artifact.Outcome{
		Type: artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{
			Kind: "registry", Host: "docker.io", Path: "org/ghost", Tag: "v1",
		},
		Push: &artifact.PushOutcome{
			Status: artifact.OutcomeSuccess, Digest: "sha256:ghost",
		},
	})

	_, err = rb.Build(&outputs)
	if err == nil {
		t.Fatal("expected Build error for orphan ArtifactID, got nil")
	}
}
