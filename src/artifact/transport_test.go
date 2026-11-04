package artifact

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

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

// Extract materializes a transport archive, and refuses one whose entries would land
// outside the publish workspace.
func TestArtifactTransport_Extract(t *testing.T) {
	write := func(entries map[string]string, link string) string {
		path := filepath.Join(t.TempDir(), "t.tar.gz")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		tw := tar.NewWriter(gz)
		for name, body := range entries {
			tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
			tw.Write([]byte(body))
		}
		if link != "" {
			tw.WriteHeader(&tar.Header{Name: "site/l", Typeflag: tar.TypeSymlink, Linkname: link})
		}
		tw.Close()
		gz.Close()
		f.Close()
		return path
	}

	ws := t.TempDir()
	if err := (ArtifactTransport{Path: write(map[string]string{"site/index.html": "hi"}, ""), Format: "tar.gz"}).Extract(ws); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "site", "index.html")); err != nil || string(b) != "hi" {
		t.Fatalf("index.html = %q %v", b, err)
	}
	if err := (ArtifactTransport{Path: write(nil, "/etc/passwd"), Format: "tar.gz"}).Extract(t.TempDir()); err == nil {
		t.Error("a symlink out of the workspace must be refused")
	}
	if err := (ArtifactTransport{Path: write(map[string]string{"../escape": "x"}, ""), Format: "tar.gz"}).Extract(t.TempDir()); err == nil {
		t.Error("a climbing path must be refused")
	}
}
