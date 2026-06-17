package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSuccessfulArchiveAssets_FiltersAndPreservesOrder(t *testing.T) {
	views := []ArchiveExecutionView{
		{ArtifactID: "archive:a", ArtifactName: "a.tar.gz", Path: "dist/a.tar.gz", SHA256: "sha:a", Size: 10, BuildStatus: OutcomeSuccess, Sources: []ArtifactID{"binary:x"}},
		{ArtifactID: "archive:b", ArtifactName: "b.tar.gz", Path: "dist/b.tar.gz", BuildStatus: OutcomeFailed},
		{ArtifactID: "archive:c", ArtifactName: "c.tar.gz", Path: "dist/c.tar.gz", SHA256: "sha:c", Size: 30, BuildStatus: OutcomeSuccess},
	}
	got := SuccessfulArchiveAssets(views)
	if len(got) != 2 {
		t.Fatalf("got %d assets, want 2 (failed excluded)", len(got))
	}
	if got[0].ArtifactID != "archive:a" || got[1].ArtifactID != "archive:c" {
		t.Fatalf("order/identity wrong: %+v", got)
	}
	if got[0].Name != "a.tar.gz" || got[0].Path != "dist/a.tar.gz" || got[0].SHA256 != "sha:a" || got[0].Size != 10 {
		t.Fatalf("field mapping wrong: %+v", got[0])
	}
	if len(got[0].Sources) != 1 || got[0].Sources[0] != "binary:x" {
		t.Fatalf("sources not carried: %+v", got[0].Sources)
	}
}

func TestSuccessfulBlobSignatureAssets_FiltersSuccessOnly(t *testing.T) {
	results := &ResultsManifest{
		IntentChecksum: "abc",
		Results: []Result{
			{ArtifactID: "checksums:SHA256SUMS", ArtifactName: "SHA256SUMS", Kind: "checksums",
				Outcomes: []Outcome{{
					Type: OutcomeTypeBlobSignature,
					BlobSignature: &BlobSignatureOutcome{
						Status: OutcomeSuccess, Kind: "cosign",
						BlobPath: "dist/SHA256SUMS", SignaturePath: "dist/SHA256SUMS.sig",
						TrustEvidence: TrustEvidence{TrustClass: "key"},
					},
				}}},
			{ArtifactID: "checksums:OTHER", ArtifactName: "OTHER", Kind: "checksums",
				Outcomes: []Outcome{{
					Type:          OutcomeTypeBlobSignature,
					BlobSignature: &BlobSignatureOutcome{Status: OutcomeFailed, Error: "boom"},
				}}},
			{ArtifactID: "archive:x", ArtifactName: "x", Kind: "archive",
				Outcomes: []Outcome{{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeSuccess, SHA256: "s", Path: "dist/x.tar.gz"}}}},
		},
	}
	got := SuccessfulBlobSignatureAssets(results)
	if len(got) != 1 {
		t.Fatalf("want 1 successful signature (failed + non-signature excluded), got %d: %+v", len(got), got)
	}
	if got[0].Path != "dist/SHA256SUMS.sig" || got[0].TrustClass != "key" || got[0].ArtifactID != "checksums:SHA256SUMS" {
		t.Errorf("field mapping wrong: %+v", got[0])
	}
	if SuccessfulBlobSignatureAssets(nil) != nil {
		t.Errorf("nil manifest must yield nil")
	}
}

// TestResolveSuccessfulArchiveAssets_ManifestSourced is the non-negotiable
// invariant for the shared archive-resolution helper: assets derive SOLELY from
// the manifests. A stray archive on disk that is NOT in the manifests must never
// appear (no globbing), and a failed archive must be excluded.
func TestResolveSuccessfulArchiveAssets_ManifestSourced(t *testing.T) {
	dir := t.TempDir()

	outputs := OutputsManifest{
		Commit: "deadbeef",
		Artifacts: []Artifact{
			{Kind: "archive", Name: "good.tar.gz", Version: "1.0.0", Archive: &ArchiveDescriptor{Format: "tar.gz", Path: "dist/good.tar.gz"}},
			{Kind: "archive", Name: "bad.tar.gz", Version: "1.0.0", Archive: &ArchiveDescriptor{Format: "tar.gz", Path: "dist/bad.tar.gz"}},
		},
	}
	if err := outputs.Finalize(); err != nil {
		t.Fatalf("outputs Finalize: %v", err)
	}
	if err := WriteOutputsManifest(dir, outputs); err != nil {
		t.Fatalf("write outputs: %v", err)
	}

	results := ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results: []Result{
			{ArtifactID: "archive:good.tar.gz", ArtifactName: "good.tar.gz", Kind: "archive",
				Outcomes: []Outcome{{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeSuccess, SHA256: "sha:good", Path: "dist/good.tar.gz", Size: 100}}}},
			{ArtifactID: "archive:bad.tar.gz", ArtifactName: "bad.tar.gz", Kind: "archive",
				Outcomes: []Outcome{{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeFailed, Error: "boom"}}}},
		},
	}
	if err := results.Finalize(); err != nil {
		t.Fatalf("results Finalize: %v", err)
	}
	if err := WriteResultsManifest(dir, results); err != nil {
		t.Fatalf("write results: %v", err)
	}

	// Stray archive on disk, NOT in any manifest. Must be ignored.
	distDir := filepath.Join(dir, ".stagefreight", "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "stray.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	assets, err := ResolveSuccessfulArchiveAssets(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("got %d assets, want 1 (good only; bad failed, stray not in manifest): %+v", len(assets), assets)
	}
	a := assets[0]
	if a.ArtifactID != "archive:good.tar.gz" || a.Name != "good.tar.gz" || a.SHA256 != "sha:good" || a.Size != 100 {
		t.Fatalf("resolved asset wrong: %+v", a)
	}
	for _, x := range assets {
		if x.Name == "stray.tar.gz" {
			t.Fatal("stray on-disk file leaked into resolution — archive resolution is not manifest-sourced")
		}
	}
}

func TestValidateRecordedDigests_RefusesDrift(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.tar.gz"), []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _ := sha256File(filepath.Join(dir, "a.tar.gz"))

	good := &ResultsManifest{Results: []Result{{ArtifactID: "archive:a",
		Outcomes: []Outcome{{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeSuccess, Path: "dist/a.tar.gz", SHA256: sum}}}}}}
	if err := ValidateRecordedDigests(good, dir); err != nil {
		t.Fatalf("matching digest must pass: %v", err)
	}

	drifted := &ResultsManifest{Results: []Result{{ArtifactID: "archive:a",
		Outcomes: []Outcome{{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeSuccess, Path: "dist/a.tar.gz", SHA256: "deadbeef"}}}}}}
	if err := ValidateRecordedDigests(drifted, dir); err == nil {
		t.Error("digest drift must be refused before signing")
	}

	missing := &ResultsManifest{Results: []Result{{ArtifactID: "archive:gone",
		Outcomes: []Outcome{{Type: OutcomeTypeArchive, Archive: &ArchiveOutcome{Status: OutcomeSuccess, Path: "dist/gone.tar.gz", SHA256: sum}}}}}}
	if err := ValidateRecordedDigests(missing, dir); err == nil {
		t.Error("a vanished artifact must be refused")
	}
}
