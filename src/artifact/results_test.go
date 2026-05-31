package artifact

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validResult() Result {
	return Result{
		ArtifactID:   "docker:stagefreight",
		ArtifactName: "stagefreight",
		Kind:         "docker",
		Outcomes: []Outcome{
			{
				Type: OutcomeTypePush,
				Target: &OutcomeTarget{
					Kind: "registry",
					Host: "docker.io",
					Path: "prplanit/stagefreight",
					Tag:  "latest-dev",
				},
				Push: &PushOutcome{
					Status:         OutcomeSuccess,
					Digest:         "sha256:abc",
					ObservedDigest: "sha256:abc",
					ObservedBy:     "buildx",
				},
			},
		},
	}
}

func TestResultsManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	manifest := ResultsManifest{
		IntentChecksum: "abc123",
		Results:        []Result{validResult()},
	}
	if err := WriteResultsManifest(dir, manifest); err != nil {
		t.Fatalf("WriteResultsManifest: %v", err)
	}
	got, err := ReadResultsManifest(dir)
	if err != nil {
		t.Fatalf("ReadResultsManifest: %v", err)
	}
	if got.SchemaVersion != ResultsSchemaVersion {
		t.Fatalf("schema_version: got %q want %q", got.SchemaVersion, ResultsSchemaVersion)
	}
	if got.IntentChecksum != "abc123" {
		t.Fatalf("intent_checksum: got %q want %q", got.IntentChecksum, "abc123")
	}
	o := got.Results[0].Outcomes[0]
	if o.Type != OutcomeTypePush {
		t.Fatalf("type: got %q want push", o.Type)
	}
	if o.Push == nil || o.Push.Status != OutcomeSuccess {
		t.Fatalf("push status: %+v", o.Push)
	}
}

func TestResultsManifestDeterministicChecksum(t *testing.T) {
	dir1, dir2 := t.TempDir(), t.TempDir()
	mk := func() ResultsManifest {
		return ResultsManifest{
			CompletedAt:    "2026-05-30T02:15:00Z",
			IntentChecksum: "abc123",
			Results:        []Result{validResult()},
		}
	}
	if err := WriteResultsManifest(dir1, mk()); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := WriteResultsManifest(dir2, mk()); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	d1, _ := os.ReadFile(filepath.Join(dir1, ResultsManifestPath))
	d2, _ := os.ReadFile(filepath.Join(dir2, ResultsManifestPath))
	if !bytes.Equal(d1, d2) {
		t.Fatalf("identical inputs produced different bytes")
	}
}

func TestResultsManifestNotFound(t *testing.T) {
	_, err := ReadResultsManifest(t.TempDir())
	if !errors.Is(err, ErrResultsManifestNotFound) {
		t.Fatalf("expected ErrResultsManifestNotFound, got %v", err)
	}
}

func TestResultsManifestMissingIntentChecksum(t *testing.T) {
	dir := t.TempDir()
	err := WriteResultsManifest(dir, ResultsManifest{
		Results: []Result{validResult()},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) || !strings.Contains(err.Error(), "intent_checksum") {
		t.Fatalf("expected intent_checksum-required error, got %v", err)
	}
}

func TestResultsManifestRejectsBadTime(t *testing.T) {
	dir := t.TempDir()
	err := WriteResultsManifest(dir, ResultsManifest{
		CompletedAt:    "yesterday",
		IntentChecksum: "abc",
		Results:        []Result{validResult()},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected invalid time error, got %v", err)
	}
}

func TestResultsManifestRejectsBadOutcomeStatus(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	r.Outcomes[0].Push.Status = "ok" // not a valid OutcomeStatus
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected invalid status error, got %v", err)
	}
}

func TestResultsManifestRejectsBadOutcomeType(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	r.Outcomes[0].Type = "publish" // not a valid OutcomeType
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected invalid outcome type error, got %v", err)
	}
}

func TestOutcomeTypeMustMatchSubPointer(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	// Type says push but Attestation is also set (or push is nil)
	r.Outcomes[0].Type = OutcomeTypeAttestation // mismatched with Push pointer
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected type/sub-outcome mismatch error, got %v", err)
	}
}

func TestMultipleSubOutcomesRejected(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	// Push is set AND Attestation is set
	r.Outcomes[0].Attestation = &AttestationOutcome{Status: OutcomeSuccess}
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected multiple sub-outcomes error, got %v", err)
	}
}

func TestOutcomeMissingSubPointerRejected(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	r.Outcomes[0].Push = nil // no sub-outcome set
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected missing-sub-outcome error, got %v", err)
	}
}

func TestPushAndAttestationOutcomesIndependent(t *testing.T) {
	// One artifact, one target, two separate outcomes: push success,
	// attestation success. They are recorded as independent facts.
	dir := t.TempDir()
	target := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/x", Tag: "v1"}
	r := Result{
		ArtifactID:   "docker:x",
		ArtifactName: "x",
		Kind:         "docker",
		Outcomes: []Outcome{
			{
				Type:   OutcomeTypePush,
				Target: &target,
				Push:   &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:abc"},
			},
			{
				Type:   OutcomeTypeAttestation,
				Target: &target,
				Attestation: &AttestationOutcome{
					Status:       OutcomeSuccess,
					Kind:         "cosign",
					SignatureRef: "docker.io/org/x:sha256-abc.sig",
				},
			},
		},
	}
	if err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadResultsManifest(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Results[0].Outcomes) != 2 {
		t.Fatalf("expected 2 independent outcomes, got %d", len(got.Results[0].Outcomes))
	}
}

func TestPushFailureDoesNotImplyAttestation(t *testing.T) {
	// Verifies that recording a push failure does NOT silently encode an
	// implied attestation outcome. The two are tracked separately.
	dir := t.TempDir()
	target := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/x", Tag: "v1"}
	r := Result{
		ArtifactID:   "docker:x",
		ArtifactName: "x",
		Kind:         "docker",
		Outcomes: []Outcome{
			{
				Type:   OutcomeTypePush,
				Target: &target,
				Push:   &PushOutcome{Status: OutcomeFailed, Error: "registry rejected: 403"},
			},
			// No attestation outcome at all — meaning "we did not attempt
			// signing", not "signing was implicitly skipped because push failed."
		},
	}
	if err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadResultsManifest(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got.Results[0].Outcomes) != 1 {
		t.Fatalf("expected exactly 1 outcome (push), got %d", len(got.Results[0].Outcomes))
	}
}

func TestResultsManifestEmptyOutcomesRejected(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	r.Outcomes = nil
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected no-outcomes error, got %v", err)
	}
}

func TestResultsManifestArtifactIDMismatch(t *testing.T) {
	dir := t.TempDir()
	r := validResult()
	r.ArtifactID = "docker:other" // doesn't match kind+name
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{r},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected artifact_id-mismatch error, got %v", err)
	}
}

func TestResultsManifestChecksumTamperDetected(t *testing.T) {
	dir := t.TempDir()
	if err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results:        []Result{validResult()},
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(dir, ResultsManifestPath)
	data, _ := os.ReadFile(path)
	tampered := bytes.Replace(data, []byte("buildx"), []byte("forged"), 1)
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadResultsManifest(dir)
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected tamper detection, got %v", err)
	}
}

func TestOutcomeStatusValid(t *testing.T) {
	for _, s := range []OutcomeStatus{OutcomeSuccess, OutcomeFailed, OutcomeSkipped} {
		if !s.Valid() {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []OutcomeStatus{"", "ok", "pass", "true", "satisfied "} {
		if s.Valid() {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestOutcomeTypeValid(t *testing.T) {
	for _, ty := range []OutcomeType{OutcomeTypePush, OutcomeTypeAttestation, OutcomeTypeBinaryBuild, OutcomeTypeArchive} {
		if !ty.Valid() {
			t.Errorf("%q should be valid", ty)
		}
	}
	for _, ty := range []OutcomeType{"", "publish", "sign", "verify"} {
		if ty.Valid() {
			t.Errorf("%q should be invalid", ty)
		}
	}
}

func TestResultsManifestMixedKindOutcomes(t *testing.T) {
	// Phase 4A validation gate: shared Finalize() stays canonicalization-
	// symmetric across all three outcome domains in one manifest.
	// Specifically: push (with target) + binary_build (no target) + archive
	// (no target with sources) all coexist; validation discriminates each
	// correctly; normalization sorts only archive Sources; checksum is
	// deterministic across runs of identical inputs.
	tgt := OutcomeTarget{Kind: "registry", Host: "docker.io", Path: "org/sf", Tag: "v1"}
	mk := func() ResultsManifest {
		return ResultsManifest{
			CompletedAt:    "2026-05-30T02:15:00Z", // fixed time for checksum stability
			IntentChecksum: "fixed-intent-checksum",
			Results: []Result{
				{
					ArtifactID: "docker:sf", ArtifactName: "sf", Kind: "docker",
					Outcomes: []Outcome{
						{
							Type: OutcomeTypePush, Target: &tgt,
							Push: &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:d", ObservedDigest: "sha256:d", ObservedBy: "buildx"},
						},
					},
				},
				{
					ArtifactID: "binary:sf-linux-amd64", ArtifactName: "sf-linux-amd64", Kind: "binary",
					Outcomes: []Outcome{
						{
							Type: OutcomeTypeBinaryBuild,
							Binary: &BinaryOutcome{Status: OutcomeSuccess, SHA256: "sha256:b", Path: "dist/sf-linux-amd64", Size: 100, BuildID: "sf"},
						},
					},
				},
				{
					ArtifactID: "archive:sf-1.0-linux-amd64.tar.gz", ArtifactName: "sf-1.0-linux-amd64.tar.gz", Kind: "archive",
					Outcomes: []Outcome{
						{
							Type: OutcomeTypeArchive,
							Archive: &ArchiveOutcome{
								Status: OutcomeSuccess, SHA256: "sha256:a", Path: "dist/sf-1.0-linux-amd64.tar.gz", Format: "tar.gz",
								// Intentionally unsorted; Finalize should sort.
								Sources: []ArtifactID{"binary:z", "binary:sf-linux-amd64", "binary:a"},
							},
						},
					},
				},
			},
		}
	}

	m1 := mk()
	if err := m1.Finalize(); err != nil {
		t.Fatalf("Finalize m1: %v", err)
	}
	m2 := mk()
	if err := m2.Finalize(); err != nil {
		t.Fatalf("Finalize m2: %v", err)
	}

	// Checksum determinism across runs of identical inputs (the gate the
	// user emphasized as the real Phase 4A validation point).
	if m1.Checksum != m2.Checksum {
		t.Fatalf("Finalize not deterministic across mixed-kind outcomes: %s vs %s", m1.Checksum, m2.Checksum)
	}

	// Archive Sources normalized to sorted order. Binary outcomes pass
	// through unchanged (no per-binary normalization — that's the
	// architectural decision the user locked in).
	archiveSources := m1.Results[2].Outcomes[0].Archive.Sources
	expectSources := []ArtifactID{"binary:a", "binary:sf-linux-amd64", "binary:z"}
	for i, want := range expectSources {
		if archiveSources[i] != want {
			t.Errorf("Sources[%d]: got %q want %q", i, archiveSources[i], want)
		}
	}

	// Roundtrip: read-back of the manifest re-validates all three kinds.
	dir := t.TempDir()
	if err := WriteResultsManifest(dir, mk()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadResultsManifest(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Results) != 3 {
		t.Fatalf("expected 3 results (docker+binary+archive), got %d", len(got.Results))
	}
	// Discriminator integrity: each result's outcome carries the right
	// sub-pointer; Type↔Target presence rule holds (push has target,
	// binary/archive don't).
	for _, r := range got.Results {
		for _, o := range r.Outcomes {
			switch o.Type {
			case OutcomeTypePush:
				if o.Push == nil || o.Target == nil {
					t.Errorf("push outcome shape broken: %+v", o)
				}
				if o.Binary != nil || o.Archive != nil || o.Attestation != nil {
					t.Errorf("push outcome has stray sub-pointers: %+v", o)
				}
			case OutcomeTypeBinaryBuild:
				if o.Binary == nil || o.Target != nil {
					t.Errorf("binary outcome shape broken: %+v", o)
				}
				if o.Push != nil || o.Archive != nil || o.Attestation != nil {
					t.Errorf("binary outcome has stray sub-pointers: %+v", o)
				}
			case OutcomeTypeArchive:
				if o.Archive == nil || o.Target != nil {
					t.Errorf("archive outcome shape broken: %+v", o)
				}
				if o.Push != nil || o.Binary != nil || o.Attestation != nil {
					t.Errorf("archive outcome has stray sub-pointers: %+v", o)
				}
			}
		}
	}
}

func TestResultsManifestRejectsTargetOnBinaryOutcome(t *testing.T) {
	// Type↔Target presence rule: binary outcomes must have nil Target.
	dir := t.TempDir()
	tgt := OutcomeTarget{Kind: "registry"}
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results: []Result{
			{
				ArtifactID: "binary:x", ArtifactName: "x", Kind: "binary",
				Outcomes: []Outcome{
					{
						Type:   OutcomeTypeBinaryBuild,
						Target: &tgt, // forbidden — binary is un-targeted by design
						Binary: &BinaryOutcome{Status: OutcomeSuccess, SHA256: "sha256:b"},
					},
				},
			},
		},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected invalid error for binary outcome with target, got %v", err)
	}
}

func TestResultsManifestRejectsMissingTargetOnPushOutcome(t *testing.T) {
	// Symmetric rule: push outcomes require non-nil Target.
	dir := t.TempDir()
	err := WriteResultsManifest(dir, ResultsManifest{
		IntentChecksum: "abc",
		Results: []Result{
			{
				ArtifactID: "docker:x", ArtifactName: "x", Kind: "docker",
				Outcomes: []Outcome{
					{
						Type: OutcomeTypePush,
						// Target intentionally nil — forbidden for push
						Push: &PushOutcome{Status: OutcomeSuccess, Digest: "sha256:d"},
					},
				},
			},
		},
	})
	if !errors.Is(err, ErrResultsManifestInvalid) {
		t.Fatalf("expected invalid error for push outcome without target, got %v", err)
	}
}
