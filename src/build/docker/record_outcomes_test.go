package docker

import (
	"context"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
)

// validIntent constructs a finalized OutputsManifest containing one docker
// artifact with the given name. Used by helper tests to give ResultsBuilder
// a real intent snapshot to validate outcomes against.
func validIntent(t *testing.T, name string) *artifact.OutputsManifest {
	t.Helper()
	m := artifact.OutputsManifest{
		Artifacts: []artifact.Artifact{
			{
				Kind: "docker",
				Name: name,
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
							Path: "org/" + name,
							Tags: []string{"v1"},
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

func TestRecordPushOutcome_FailureStatus_RecordsFailureShape(t *testing.T) {
	// The helper's failure-status branch is the structurally important
	// path: it must record a push Outcome carrying status + error without
	// touching the network. This proves the "status flows from caller,
	// not assumed from post-block position" invariant.
	rb := build.NewResultsBuilder()
	target := artifact.OutcomeTarget{
		Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1",
	}
	digest := recordPushOutcome(
		context.Background(), rb, "docker:sf", target,
		artifact.OutcomeFailed, "", "", "registry refused: 403",
	)
	if digest != "" {
		t.Fatalf("expected empty digest on failure path, got %q", digest)
	}
	results, err := rb.Build(validIntent(t, "sf"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(results.Results[0].Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(results.Results[0].Outcomes))
	}
	o := results.Results[0].Outcomes[0]
	if o.Type != artifact.OutcomeTypePush {
		t.Fatalf("type: got %q want push", o.Type)
	}
	if o.Push == nil {
		t.Fatal("Push pointer nil — discriminator/sub-pointer mismatch")
	}
	if o.Push.Status != artifact.OutcomeFailed {
		t.Fatalf("status: got %q want failed", o.Push.Status)
	}
	if o.Push.Error != "registry refused: 403" {
		t.Fatalf("error: got %q", o.Push.Error)
	}
	if o.Push.Digest != "" {
		t.Fatalf("expected empty digest in failure outcome, got %q", o.Push.Digest)
	}
	if o.Attestation != nil {
		t.Fatal("attestation should be nil — discriminated union invariant")
	}
}

func TestRecordPushOutcome_SkippedStatus_RecordsSkippedShape(t *testing.T) {
	rb := build.NewResultsBuilder()
	target := artifact.OutcomeTarget{
		Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1",
	}
	_ = recordPushOutcome(
		context.Background(), rb, "docker:sf", target,
		artifact.OutcomeSkipped, "", "", "registry disabled by policy",
	)
	results, err := rb.Build(validIntent(t, "sf"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	o := results.Results[0].Outcomes[0]
	if o.Push.Status != artifact.OutcomeSkipped {
		t.Fatalf("status: got %q want skipped", o.Push.Status)
	}
}

func TestRecordPushOutcome_FailureDoesNotPropagateCapturedDigest(t *testing.T) {
	// On failure, capturedDigest from the caller is intentionally ignored —
	// recording a digest on a failed push would falsely claim the manifest
	// was published. Verify it doesn't leak through.
	rb := build.NewResultsBuilder()
	target := artifact.OutcomeTarget{
		Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1",
	}
	_ = recordPushOutcome(
		context.Background(), rb, "docker:sf", target,
		artifact.OutcomeFailed, "sha256:doomed", "", "push failed",
	)
	results, _ := rb.Build(validIntent(t, "sf"))
	o := results.Results[0].Outcomes[0]
	if o.Push.Digest != "" {
		t.Fatalf("captured digest leaked into failure outcome: %q", o.Push.Digest)
	}
}

func TestRecordAttestationOutcomeIfConfigured_NoKey_RecordsNothing(t *testing.T) {
	// Signing not configured (empty cosignKey) → no outcome at all.
	// Absence in the results manifest means "not attempted," never
	// "implicit skip." This is the architectural invariant the user
	// flagged as load-bearing.
	rb := build.NewResultsBuilder()
	target := artifact.OutcomeTarget{
		Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1",
	}
	recordAttestationOutcomeIfConfigured(
		context.Background(), rb, "docker:sf", target,
		"sha256:abc", false, "/tmp", "", nil, "/tmp/nonexistent.dsse.json", "",
	)
	results, err := rb.Build(validIntent(t, "sf"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(results.Results) != 0 {
		t.Fatalf("expected zero results (no outcomes recorded), got %d", len(results.Results))
	}
}

func TestRecordAttestationOutcomeIfConfigured_NoDigest_RecordsNothing(t *testing.T) {
	// Signing configured but no digest available → no outcome.
	// Same absence semantics as no-key.
	rb := build.NewResultsBuilder()
	target := artifact.OutcomeTarget{
		Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1",
	}
	recordAttestationOutcomeIfConfigured(
		context.Background(), rb, "docker:sf", target,
		"", false, "/tmp", "/path/to/key", nil, "/tmp/nonexistent.dsse.json", "",
	)
	results, _ := rb.Build(validIntent(t, "sf"))
	if len(results.Results) != 0 {
		t.Fatalf("expected zero results for empty-digest skip path, got %d", len(results.Results))
	}
}

func TestRecordAttestationOutcomeIfConfigured_AbsenceMeansNotAttempted(t *testing.T) {
	// Combined invariant: a recorded push outcome MUST NOT imply that a
	// matching attestation outcome was attempted. If signing is skipped,
	// the push outcome is the only outcome for that target.
	rb := build.NewResultsBuilder()
	target := artifact.OutcomeTarget{
		Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1",
	}
	_ = recordPushOutcome(
		context.Background(), rb, "docker:sf", target,
		artifact.OutcomeFailed, "", "", "push refused",
	)
	recordAttestationOutcomeIfConfigured(
		context.Background(), rb, "docker:sf", target,
		"", false, "/tmp", "", nil, "/tmp/missing.dsse.json", "",
	)
	results, _ := rb.Build(validIntent(t, "sf"))
	if len(results.Results[0].Outcomes) != 1 {
		t.Fatalf("expected exactly 1 outcome (push only), got %d", len(results.Results[0].Outcomes))
	}
	if results.Results[0].Outcomes[0].Type != artifact.OutcomeTypePush {
		t.Fatalf("expected push outcome, got %q", results.Results[0].Outcomes[0].Type)
	}
}
