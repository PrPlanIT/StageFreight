package artifact

import (
	"reflect"
	"sort"
	"testing"
)

// binaryArchiveOutputs returns a finalized OutputsManifest with one binary
// artifact and one archive artifact. Used by both BinaryExecutionView and
// ArchiveExecutionView tests.
func binaryArchiveOutputs(t *testing.T) *OutputsManifest {
	t.Helper()
	m := OutputsManifest{
		Commit: "30d3da2d",
		Artifacts: []Artifact{
			{
				Kind:    "binary",
				Name:    "sf-cli-linux-amd64",
				Version: "1.0.0",
				Binary: &BinaryDescriptor{
					OS:        "linux",
					Arch:      "amd64",
					Path:      "dist/sf-cli-linux-amd64",
					Toolchain: "go1.24.1",
				},
			},
			{
				Kind:    "archive",
				Name:    "sf-cli-1.0.0-linux-amd64.tar.gz",
				Version: "1.0.0",
				Archive: &ArchiveDescriptor{
					Format: "tar.gz",
					Path:   "dist/sf-cli-1.0.0-linux-amd64.tar.gz",
				},
			},
		},
	}
	if err := m.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return &m
}

func TestBuildBinaryExecutionViews_SuccessfulBuild(t *testing.T) {
	outputs := binaryArchiveOutputs(t)
	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID:   "binary:sf-cli-linux-amd64",
				ArtifactName: "sf-cli-linux-amd64",
				Kind:         "binary",
				Outcomes: []Outcome{
					{
						Type: OutcomeTypeBinaryBuild,
						Binary: &BinaryOutcome{
							Status:  OutcomeSuccess,
							SHA256:  "sha256:abc",
							Path:    "dist/sf-cli-linux-amd64",
							Size:    12345678,
							BuildID: "sf-cli",
						},
					},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	views := BuildBinaryExecutionViews(outputs, &results)
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	v := views[0]
	if v.ArtifactID != "binary:sf-cli-linux-amd64" || v.ArtifactKind != "binary" {
		t.Fatalf("identity: %+v", v)
	}
	if v.OS != "linux" || v.Arch != "amd64" || v.Toolchain != "go1.24.1" {
		t.Fatalf("descriptor fields: %+v", v)
	}
	if v.BuildStatus != OutcomeSuccess || v.SHA256 != "sha256:abc" || v.Size != 12345678 || v.BuildID != "sf-cli" {
		t.Fatalf("outcome fields: %+v", v)
	}
	if v.ExpectedCommit != "30d3da2d" {
		t.Fatalf("expected_commit: %q", v.ExpectedCommit)
	}
}

func TestBuildBinaryExecutionViews_FailedBuildSurfaces(t *testing.T) {
	outputs := binaryArchiveOutputs(t)
	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID:   "binary:sf-cli-linux-amd64",
				ArtifactName: "sf-cli-linux-amd64",
				Kind:         "binary",
				Outcomes: []Outcome{
					{
						Type: OutcomeTypeBinaryBuild,
						Binary: &BinaryOutcome{
							Status: OutcomeFailed,
							Error:  "compile error: undeclared name",
						},
					},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	views := BuildBinaryExecutionViews(outputs, &results)
	if len(views) != 1 {
		t.Fatalf("expected 1 view for failed build, got %d", len(views))
	}
	if views[0].BuildStatus != OutcomeFailed {
		t.Fatalf("status: got %q", views[0].BuildStatus)
	}
	if views[0].SHA256 != "" {
		t.Fatalf("failed build should have empty SHA256, got %q", views[0].SHA256)
	}
	if views[0].BuildError == "" {
		t.Fatal("BuildError should be populated for failed build")
	}
}

func TestBuildBinaryExecutionViews_NonBinaryResultsIgnored(t *testing.T) {
	outputs := binaryArchiveOutputs(t)
	// Results contains both binary and archive outcomes; ensure the
	// binary view builder only surfaces binary results.
	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID: "binary:sf-cli-linux-amd64", ArtifactName: "sf-cli-linux-amd64", Kind: "binary",
				Outcomes: []Outcome{
					{Type: OutcomeTypeBinaryBuild, Binary: &BinaryOutcome{Status: OutcomeSuccess, SHA256: "sha256:bin"}},
				},
			},
			{
				ArtifactID: "archive:sf-cli-1.0.0-linux-amd64.tar.gz", ArtifactName: "sf-cli-1.0.0-linux-amd64.tar.gz", Kind: "archive",
				Outcomes: []Outcome{
					{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeSuccess, SHA256: "sha256:arc"}},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	views := BuildBinaryExecutionViews(outputs, &results)
	if len(views) != 1 {
		t.Fatalf("expected 1 binary view, got %d", len(views))
	}
	if views[0].ArtifactKind != "binary" {
		t.Fatalf("expected binary view, got %q", views[0].ArtifactKind)
	}
}

func TestBuildArchiveExecutionViews_SourcesAreSiblingReferences(t *testing.T) {
	// Architectural invariant: ArchiveExecutionView.Sources holds binary
	// ArtifactIDs as opaque strings. No embedded binary fields, no resolution.
	outputs := binaryArchiveOutputs(t)
	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID: "archive:sf-cli-1.0.0-linux-amd64.tar.gz", ArtifactName: "sf-cli-1.0.0-linux-amd64.tar.gz", Kind: "archive",
				Outcomes: []Outcome{
					{
						Type: OutcomeTypeArchive,
						Archive: &ArchiveOutcome{
							Status:  OutcomeSuccess,
							SHA256:  "sha256:arc",
							Path:    "dist/sf-cli-1.0.0-linux-amd64.tar.gz",
							Format:  "tar.gz",
							Sources: []ArtifactID{"binary:sf-cli-linux-amd64"},
						},
					},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	views := BuildArchiveExecutionViews(outputs, &results)
	if len(views) != 1 {
		t.Fatalf("expected 1 archive view, got %d", len(views))
	}
	v := views[0]
	if len(v.Sources) != 1 || v.Sources[0] != "binary:sf-cli-linux-amd64" {
		t.Fatalf("sources: %v", v.Sources)
	}
	if v.Format != "tar.gz" || v.SHA256 != "sha256:arc" {
		t.Fatalf("archive fields: %+v", v)
	}
}

func TestBuildArchiveExecutionViews_SourcesNormalizedByFinalize(t *testing.T) {
	// Finalize sorts Sources deterministically for canonical serialization.
	// Consumer-facing view reflects the post-Finalize ordering, but
	// consumers MUST treat Sources as an unordered set regardless.
	outputs := binaryArchiveOutputs(t)
	// Add a second binary intent so multi-source archive is valid
	outputs2 := *outputs
	outputs2.Checksum = ""
	outputs2.Artifacts = append(outputs2.Artifacts, Artifact{
		Kind: "binary", Name: "sf-cli-linux-arm64", Version: "1.0.0",
		Binary: &BinaryDescriptor{OS: "linux", Arch: "arm64", Path: "dist/sf-cli-linux-arm64", Toolchain: "go1.24.1"},
	})
	if err := outputs2.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	results := ResultsManifest{
		IntentChecksum: outputs2.Checksum,
		Results: []Result{
			{
				ArtifactID: "archive:sf-cli-1.0.0-linux-amd64.tar.gz", ArtifactName: "sf-cli-1.0.0-linux-amd64.tar.gz", Kind: "archive",
				Outcomes: []Outcome{
					{
						Type: OutcomeTypeArchive,
						Archive: &ArchiveOutcome{
							Status: OutcomeSuccess, SHA256: "sha256:arc",
							// Insertion order: arm64 first
							Sources: []ArtifactID{"binary:sf-cli-linux-arm64", "binary:sf-cli-linux-amd64"},
						},
					},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	views := BuildArchiveExecutionViews(&outputs2, &results)
	if len(views) != 1 {
		t.Fatalf("expected 1 view")
	}
	gotSorted := make([]ArtifactID, len(views[0].Sources))
	copy(gotSorted, views[0].Sources)
	sort.Slice(gotSorted, func(i, j int) bool { return gotSorted[i] < gotSorted[j] })
	if !reflect.DeepEqual(views[0].Sources, gotSorted) {
		t.Fatalf("Sources not deterministically sorted by Finalize: %v", views[0].Sources)
	}
}

func TestBuildBinaryAndArchiveViews_NoCrossViewLookup(t *testing.T) {
	// Architectural rule: view builders do NOT resolve dependencies.
	// ArchiveExecutionView.Sources references binary ArtifactIDs as
	// opaque strings, even when the referenced binaries don't exist in
	// the manifests. Consumers handle joins.
	outputs := OutputsManifest{
		Artifacts: []Artifact{
			{
				Kind: "archive", Name: "orphan.tar.gz", Version: "1.0.0",
				Archive: &ArchiveDescriptor{Format: "tar.gz", Path: "dist/orphan.tar.gz"},
			},
		},
	}
	if err := outputs.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID: "archive:orphan.tar.gz", ArtifactName: "orphan.tar.gz", Kind: "archive",
				Outcomes: []Outcome{
					{
						Type: OutcomeTypeArchive,
						Archive: &ArchiveOutcome{
							Status: OutcomeSuccess, SHA256: "sha256:o",
							Sources: []ArtifactID{"binary:does-not-exist"},
						},
					},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	// View builder accepts the orphan ArtifactID without complaint —
	// no cross-domain validation at the view layer.
	views := BuildArchiveExecutionViews(&outputs, &results)
	if len(views) != 1 || views[0].Sources[0] != "binary:does-not-exist" {
		t.Fatalf("orphan source not surfaced verbatim: %+v", views)
	}
}

func TestBuildBinaryExecutionViews_NilInputsReturnsNil(t *testing.T) {
	if v := BuildBinaryExecutionViews(nil, nil); v != nil {
		t.Fatalf("expected nil for nil inputs, got %v", v)
	}
	if v := BuildArchiveExecutionViews(nil, nil); v != nil {
		t.Fatalf("expected nil for nil inputs, got %v", v)
	}
}
