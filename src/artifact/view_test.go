package artifact

import (
	"testing"
)

// minimalOutputs returns a finalized OutputsManifest with one docker artifact
// "sf" published to docker.io/org/sf with tags v1, v2.
func minimalOutputs(t *testing.T) *OutputsManifest {
	t.Helper()
	m := OutputsManifest{
		Commit: "30d3da2d",
		Artifacts: []Artifact{
			{
				Kind:    "docker",
				Name:    "sf",
				Version: "1.0.0",
				Docker: &DockerDescriptor{
					Dockerfile: "Dockerfile",
					Context:    ".",
					Platforms:  []string{"linux/amd64"},
				},
				Targets: []Target{
					{
						Kind: "registry",
						Registry: &RegistryTarget{
							Host: "docker.io",
							Path: "org/sf",
							Tags: []string{"v1", "v2"},
						},
						Requirements: Requirements{Sign: true},
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

func resultsWith(t *testing.T, outputs *OutputsManifest, outcomes []Outcome) *ResultsManifest {
	t.Helper()
	m := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID:   "docker:sf",
				ArtifactName: "sf",
				Kind:         "docker",
				Outcomes:     outcomes,
			},
		},
	}
	if err := m.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return &m
}

func TestBuildPublicationViews_OnePushOneTag(t *testing.T) {
	outputs := minimalOutputs(t)
	results := resultsWith(t, outputs, []Outcome{
		{
			Type:   OutcomeTypePush,
			Target: &OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"},
			Push:   &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc", ObservedDigest: "sha256:abc", ObservedBy: "buildx"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	v := views[0]
	if v.ArtifactID != "docker:sf" || v.ArtifactKind != "docker" || v.ArtifactName != "sf" {
		t.Fatalf("identity wrong: %+v", v)
	}
	if v.Version != "1.0.0" {
		t.Fatalf("version: got %q want 1.0.0", v.Version)
	}
	if v.Ref() != "docker.io/org/sf:v1" {
		t.Fatalf("ref: got %q", v.Ref())
	}
	if v.DigestRef() != "docker.io/org/sf@sha256:abc" {
		t.Fatalf("digestRef: got %q", v.DigestRef())
	}
	if v.ExpectedCommit != "30d3da2d" {
		t.Fatalf("expected_commit: got %q", v.ExpectedCommit)
	}
	if len(v.ExpectedTags) != 2 {
		t.Fatalf("expected_tags: got %v", v.ExpectedTags)
	}
	if !v.Requirements.Sign {
		t.Fatalf("requirements.sign not propagated")
	}
	if v.SigningAttempted {
		t.Fatalf("SigningAttempted should be false with no attestation outcome")
	}
	if v.Attestation != nil {
		t.Fatalf("Attestation should be nil")
	}
}

func TestBuildPublicationViews_SigningAttemptedDerivedFromAttestation(t *testing.T) {
	outputs := minimalOutputs(t)
	tgt := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"}
	results := resultsWith(t, outputs, []Outcome{
		{
			Type: OutcomeTypePush, Target: &tgt,
			Push: &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc"},
		},
		{
			Type: OutcomeTypeAttestation, Target: &tgt,
			Attestation: &AttestationOutcome{Status: OutcomeSuccess, Kind: "cosign", SignatureRef: "docker.io/org/sf:sha256-abc.sig"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if !views[0].SigningAttempted {
		t.Fatalf("SigningAttempted should be true when attestation outcome exists")
	}
	if views[0].Attestation == nil || views[0].Attestation.Status != OutcomeSuccess {
		t.Fatalf("Attestation not joined: %+v", views[0].Attestation)
	}
}

func TestBuildPublicationViews_AttestationFailedStillCountsAsAttempted(t *testing.T) {
	outputs := minimalOutputs(t)
	tgt := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"}
	results := resultsWith(t, outputs, []Outcome{
		{
			Type: OutcomeTypePush, Target: &tgt,
			Push: &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc"},
		},
		{
			Type: OutcomeTypeAttestation, Target: &tgt,
			Attestation: &AttestationOutcome{Status: OutcomeFailed, Kind: "cosign", Error: "no key"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if !views[0].SigningAttempted {
		t.Fatal("SigningAttempted should be true even when attestation failed")
	}
	if views[0].Attestation.Status != OutcomeFailed {
		t.Fatalf("status: got %q", views[0].Attestation.Status)
	}
}

func TestBuildPublicationViews_NoAttestationOutcomeMeansNotAttempted(t *testing.T) {
	// Architectural invariant: absence of attestation outcome MUST be
	// surfaced as SigningAttempted=false. Never assumed true from push.
	outputs := minimalOutputs(t)
	tgt := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"}
	results := resultsWith(t, outputs, []Outcome{
		{
			Type: OutcomeTypePush, Target: &tgt,
			Push: &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if views[0].SigningAttempted {
		t.Fatal("SigningAttempted must be false when no attestation outcome exists")
	}
}

func TestBuildPublicationViews_FailedPushStillSurfaces(t *testing.T) {
	// Failed push outcomes must surface in the view — consumers filter
	// based on their own narrowing. Default view is broad.
	outputs := minimalOutputs(t)
	tgt := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"}
	results := resultsWith(t, outputs, []Outcome{
		{
			Type: OutcomeTypePush, Target: &tgt,
			Push: &PushOutcome{Status: OutcomeFailed, Error: "registry 403"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if len(views) != 1 {
		t.Fatalf("expected failed-push view to surface")
	}
	v := views[0]
	if v.PushStatus != OutcomeFailed {
		t.Fatalf("status: got %q", v.PushStatus)
	}
	if v.Digest != "" {
		t.Fatalf("failed push should have empty digest, got %q", v.Digest)
	}
	if v.PushError != "registry 403" {
		t.Fatalf("error: got %q", v.PushError)
	}
	if v.DigestRef() != "" {
		t.Fatalf("digestRef on failed push must be empty: %q", v.DigestRef())
	}
}

func TestBuildPublicationViews_MultipleTagsOneArtifact(t *testing.T) {
	outputs := minimalOutputs(t)
	results := resultsWith(t, outputs, []Outcome{
		{
			Type:   OutcomeTypePush,
			Target: &OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"},
			Push:   &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc"},
		},
		{
			Type:   OutcomeTypePush,
			Target: &OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v2"},
			Push:   &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
	if views[0].Tag != "v1" || views[1].Tag != "v2" {
		t.Fatalf("tag ordering not preserved: %s, %s", views[0].Tag, views[1].Tag)
	}
	for i, v := range views {
		if len(v.ExpectedTags) != 2 {
			t.Fatalf("view[%d] expected_tags: got %v", i, v.ExpectedTags)
		}
	}
}

func TestBuildPublicationViews_NilInputsReturnsNil(t *testing.T) {
	if v := BuildPublicationViews(nil, nil); v != nil {
		t.Fatalf("expected nil for nil inputs, got %v", v)
	}
	outputs := minimalOutputs(t)
	if v := BuildPublicationViews(outputs, nil); v != nil {
		t.Fatalf("expected nil when results nil, got %v", v)
	}
}

func TestBuildPublicationViews_NonPushOutcomesIgnored(t *testing.T) {
	outputs := minimalOutputs(t)
	tgt := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"}
	results := resultsWith(t, outputs, []Outcome{
		{
			Type: OutcomeTypeAttestation, Target: &tgt,
			Attestation: &AttestationOutcome{Status: OutcomeSuccess, Kind: "cosign"},
		},
	})
	views := BuildPublicationViews(outputs, results)
	if len(views) != 0 {
		t.Fatalf("expected 0 views (only attestation outcome), got %d", len(views))
	}
}
