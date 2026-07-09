package artifact

import "testing"

// The transport resolver is the seam between perform (which archives a build's tree
// into ManagedRoot) and publish (which deploys it): given a build id it must return
// the archive whose Sources reference that build's binary artifact.
func TestResolveBuildTransport_JoinsBuildToArchive(t *testing.T) {
	outputs := binaryArchiveOutputs(t) // one binary + one archive artifact
	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{
				ArtifactID:   "binary:sf-cli-linux-amd64",
				ArtifactName: "sf-cli-linux-amd64",
				Kind:         "binary",
				Outcomes: []Outcome{
					{Type: OutcomeTypeBinaryBuild, Binary: &BinaryOutcome{
						Status: OutcomeSuccess, Path: "dist/sf-cli-linux-amd64", BuildID: "site",
					}},
				},
			},
			{
				ArtifactID:   "archive:sf-cli-1.0.0-linux-amd64.tar.gz",
				ArtifactName: "sf-cli-1.0.0-linux-amd64.tar.gz",
				Kind:         "archive",
				Outcomes: []Outcome{
					{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{
						Status:  OutcomeSuccess,
						Path:    "dist/sf-cli-1.0.0-linux-amd64.tar.gz",
						Format:  "tar.gz",
						Sources: []ArtifactID{"binary:sf-cli-linux-amd64"},
					}},
				},
			},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	tr, err := resolveBuildTransport(outputs, &results, "site")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tr.Path != "dist/sf-cli-1.0.0-linux-amd64.tar.gz" {
		t.Errorf("transport path = %q, want the archive path", tr.Path)
	}
	if tr.Format != "tar.gz" {
		t.Errorf("format = %q, want tar.gz", tr.Format)
	}

	// A build with no archive under ManagedRoot fails loudly.
	if _, err := resolveBuildTransport(outputs, &results, "no-such-build"); err == nil {
		t.Error("expected an error for a build that has no transport archive")
	}
}

func TestArchiveFormatOf(t *testing.T) {
	if archiveFormatOf("x/y.zip") != "zip" {
		t.Error("zip not detected")
	}
	if archiveFormatOf("x/y.tar.gz") != "tar.gz" {
		t.Error("tar.gz default expected")
	}
}
