package build

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// sampleOutputs returns an in-memory finalized OutputsManifest — no disk
// round-trip. ResultsBuilder.Build accepts the intent snapshot directly per
// the architecture's no-cross-phase-file-reads rule.
func sampleOutputs(t *testing.T) *artifact.OutputsManifest {
	t.Helper()
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{
			{
				Kind: "docker",
				Name: "sf",
				Docker: &artifact.DockerDescriptor{
					Dockerfile: "Dockerfile",
					Context:    ".",
					Platforms:  []string{"linux/amd64"},
				},
				Targets: []artifact.Target{
					{
						Kind: "registry",
						Registry: &artifact.RegistryTarget{
							Host: "docker.io",
							Path: "prplanit/sf",
							Tags: []string{"latest-dev"},
						},
					},
				},
			},
		},
	}
	if err := m.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return &m
}

// TestPromotionPreservesArchiveOutcomes is the regression guard for the clobber
// bug: publish promotion must EXTEND published.json (add docker push outcomes),
// not replace it with a docker-only manifest. If it replaces, the archive BUILD
// outcomes perform recorded vanish and release-asset discovery attaches nothing.
func TestPromotionPreservesArchiveOutcomes(t *testing.T) {
	// outputs: a docker image (promoted in publish) + an archive (built in perform).
	out := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{
			{
				Kind: "docker", Name: "sf",
				Docker:  &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
				Targets: []artifact.Target{{Kind: "registry", Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "prplanit/sf", Tags: []string{"latest-dev"}}}},
			},
			{
				Kind: "archive", Name: "sf-linux-amd64.tar.gz", Version: "1.0.0",
				Archive: &artifact.ArchiveDescriptor{Format: "tar.gz", Path: ".stagefreight/dist/sf-linux-amd64.tar.gz"},
			},
		},
	}
	if err := out.Finalize(); err != nil {
		t.Fatalf("Finalize outputs: %v", err)
	}

	// perform-written results: the archive built successfully.
	perform := artifact.ResultsManifest{
		IntentChecksum: out.Checksum,
		Results: []artifact.Result{
			{
				ArtifactID: "archive:sf-linux-amd64.tar.gz", ArtifactName: "sf-linux-amd64.tar.gz", Kind: "archive",
				Outcomes: []artifact.Outcome{{
					Type:    artifact.OutcomeTypeArchive,
					Archive: &artifact.ArchiveOutcome{Status: artifact.OutcomeSuccess, SHA256: "sha256:arc", Path: ".stagefreight/dist/sf-linux-amd64.tar.gz", Format: "tar.gz", Size: 100},
				}},
			},
		},
	}
	if err := perform.Finalize(); err != nil {
		t.Fatalf("Finalize perform results: %v", err)
	}

	dockerPush := artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "prplanit/sf", Tag: "latest-dev"},
		Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess, Digest: "sha256:img"},
	}

	// Promotion: seed from perform, add the docker push outcome (the fix).
	rb := ResultsBuilderFromManifest(&perform)
	rb.Record("docker:sf", dockerPush)
	merged, err := rb.Build(&out)
	if err != nil {
		t.Fatalf("Build merged: %v", err)
	}

	// Invariant 1: the archive build outcome survives → discovery still sees it.
	views := artifact.BuildArchiveExecutionViews(&out, &merged)
	if len(views) != 1 || views[0].BuildStatus != artifact.OutcomeSuccess {
		t.Fatalf("archive outcome did not survive promotion — release would attach nothing: %+v", views)
	}

	// Invariant 2: the docker push outcome was added.
	foundPush := false
	for _, r := range merged.Results {
		if r.ArtifactID != "docker:sf" {
			continue
		}
		for _, o := range r.Outcomes {
			if o.Type == artifact.OutcomeTypePush {
				foundPush = true
			}
		}
	}
	if !foundPush {
		t.Fatal("docker push outcome missing from merged manifest")
	}

	// Negative control: the OLD behavior (fresh builder, docker-only) yields NO
	// successful archive view — the exact failure this test guards against.
	old := NewResultsBuilder()
	old.Record("docker:sf", dockerPush)
	oldResults, err := old.Build(&out)
	if err != nil {
		t.Fatalf("Build old: %v", err)
	}
	for _, v := range artifact.BuildArchiveExecutionViews(&out, &oldResults) {
		if v.BuildStatus == artifact.OutcomeSuccess {
			t.Fatal("negative control: docker-only manifest must NOT yield a successful archive view")
		}
	}
}

func TestResultsBuilderRecordAndBuild(t *testing.T) {
	out := sampleOutputs(t)
	rb := NewResultsBuilder()
	rb.Record("docker:sf", artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "prplanit/sf", Tag: "latest-dev"},
		Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess, Digest: "sha256:abc"},
	})

	results, err := rb.Build(out)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if results.IntentChecksum != out.Checksum {
		t.Fatalf("intent_checksum: got %q want %q", results.IntentChecksum, out.Checksum)
	}
	if len(results.Results) != 1 {
		t.Fatalf("results: got %d want 1", len(results.Results))
	}
	r := results.Results[0]
	if r.ArtifactID != "docker:sf" || r.ArtifactName != "sf" || r.Kind != "docker" {
		t.Fatalf("result join failed: %+v", r)
	}
	if len(r.Outcomes) != 1 || r.Outcomes[0].Push == nil || r.Outcomes[0].Push.Digest != "sha256:abc" {
		t.Fatalf("outcome not preserved: %+v", r.Outcomes)
	}
}

func TestResultsBuilderRecordedManifestPersists(t *testing.T) {
	// Round-trip the results manifest through Write/Read to confirm the
	// schema produced by the builder is valid.
	dir := t.TempDir()
	out := sampleOutputs(t)
	rb := NewResultsBuilder()
	rb.Record("docker:sf", artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "prplanit/sf", Tag: "latest-dev"},
		Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess},
	})
	results, err := rb.Build(out)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := artifact.WriteResultsManifest(dir, results); err != nil {
		t.Fatalf("write results: %v", err)
	}
	got, err := artifact.ReadResultsManifest(dir)
	if err != nil {
		t.Fatalf("read results: %v", err)
	}
	if got.IntentChecksum != out.Checksum {
		t.Fatalf("intent_checksum after round-trip mismatch")
	}
}

func TestResultsBuilderDeterministicOrder(t *testing.T) {
	// Two artifacts in outputs; results recorded in a specific order should
	// be reproduced in Build's output (first-recorded order).
	out := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{
			{
				Kind: "docker", Name: "a",
				Docker: &artifact.DockerDescriptor{Dockerfile: "Dockerfile", Context: ".", Platforms: []string{"linux/amd64"}},
				Targets: []artifact.Target{{
					Kind:     "registry",
					Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "org/a", Tags: []string{"v1"}},
				}},
			},
			{
				Kind: "docker", Name: "b",
				Docker: &artifact.DockerDescriptor{Dockerfile: "Dockerfile.b", Context: ".", Platforms: []string{"linux/amd64"}},
				Targets: []artifact.Target{{
					Kind:     "registry",
					Registry: &artifact.RegistryTarget{Host: "docker.io", Path: "org/b", Tags: []string{"v1"}},
				}},
			},
		},
	}
	if err := out.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// Record b first, then a — Build's order should be (b, a).
	rb := NewResultsBuilder()
	rb.Record("docker:b", artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{Kind: "registry"},
		Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess},
	})
	rb.Record("docker:a", artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{Kind: "registry"},
		Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess},
	})
	results, err := rb.Build(&out)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if results.Results[0].ArtifactID != "docker:b" || results.Results[1].ArtifactID != "docker:a" {
		t.Fatalf("first-recorded order not preserved: %q, %q",
			results.Results[0].ArtifactID, results.Results[1].ArtifactID)
	}
}

func TestResultsBuilderRejectsUnknownArtifactID(t *testing.T) {
	out := sampleOutputs(t)
	rb := NewResultsBuilder()
	rb.Record("docker:not-in-outputs", artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &artifact.OutcomeTarget{Kind: "registry"},
		Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess},
	})
	_, err := rb.Build(out)
	if err == nil || !strings.Contains(err.Error(), "unknown artifact id") {
		t.Fatalf("expected unknown-id error, got %v", err)
	}
}

func TestResultsBuilderRejectsNilOutputs(t *testing.T) {
	rb := NewResultsBuilder()
	_, err := rb.Build(nil)
	if err == nil {
		t.Fatal("expected error on nil outputs")
	}
}

func TestResultsBuilderRejectsUnchecksumedOutputs(t *testing.T) {
	rb := NewResultsBuilder()
	// Manifest with no checksum populated — caller forgot to round-trip via Write/Read.
	out := &artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{
			{Kind: "docker", Name: "sf", ID: "docker:sf"},
		},
	}
	_, err := rb.Build(out)
	if err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Fatalf("expected no-checksum error, got %v", err)
	}
}

func TestResultsBuilderMultipleOutcomesPerArtifact(t *testing.T) {
	out := sampleOutputs(t)
	rb := NewResultsBuilder()
	for _, tag := range []string{"latest-dev", "dev-abc", "dev-def"} {
		rb.Record("docker:sf", artifact.Outcome{
			Type:   artifact.OutcomeTypePush,
			Target: &artifact.OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "prplanit/sf", Tag: tag},
			Push:   &artifact.PushOutcome{Status: artifact.OutcomeSuccess},
		})
	}
	results, err := rb.Build(out)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(results.Results[0].Outcomes) != 3 {
		t.Fatalf("expected 3 outcomes, got %d", len(results.Results[0].Outcomes))
	}
	// Append order preserved
	if results.Results[0].Outcomes[0].Target.Tag != "latest-dev" ||
		results.Results[0].Outcomes[2].Target.Tag != "dev-def" {
		t.Fatalf("append order not preserved: %+v", results.Results[0].Outcomes)
	}
}
