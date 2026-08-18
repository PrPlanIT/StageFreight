package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/forge"
)

// classifyAssets sorts source assets into the three buckets of the mirror model.
func TestClassifyAssets_SplitsFilesLinksDiagnostics(t *testing.T) {
	files, links, diags := classifyAssets([]forge.ReleaseAsset{
		{Name: "binary.zip", URL: "https://gitlab.example.com/dl/binary.zip", Size: 100, Digest: "sha256:x"}, // file
		{Name: "quay v0.0.5", URL: "https://quay.io/repository/x", External: true, LinkType: "image"},        // link
		{Name: "broken", External: true, URL: ""},                                                            // unsupported
	})
	if len(files) != 1 || files[0].Name != "binary.zip" {
		t.Fatalf("files = %+v, want [binary.zip]", files)
	}
	if len(links) != 1 || links[0].Name != "quay v0.0.5" || links[0].URL != "https://quay.io/repository/x" {
		t.Fatalf("links = %+v, want [quay v0.0.5→quay.io]", links)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want one unsupported diagnostic", diags)
	}
}

// Reconcile must UPLOAD file assets and RE-CREATE external links via AddReleaseLink — and
// must NEVER download a link (the fake's DownloadReleaseAsset would error for it).
func TestReconcile_FilesUploadedLinksAddedNeverDownloaded(t *testing.T) {
	local := filepath.Join(t.TempDir(), "app.bin")
	if err := os.WriteFile(local, []byte("binary-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := DesiredRelease{
		Tag: "v1", Name: "v1", Body: "notes",
		Assets: []DesiredAsset{{Name: "app.bin", Local: local}},
		Links:  []DesiredLink{{Name: "quay v1", URL: "https://quay.io/repository/x", LinkType: "image"}},
	}
	src, dst := newFake("src"), newFake("dst")

	res, err := ReconcileReleases(context.Background(), src, dst, []DesiredRelease{d}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", res.Errors)
	}
	rel := dst.rels["v1"]
	if rel == nil {
		t.Fatal("mirror release not created")
	}
	if _, ok := rel.assets["app.bin"]; !ok {
		t.Errorf("file asset app.bin was not uploaded; uploads=%v", dst.uploads)
	}
	if _, ok := rel.links["quay v1"]; !ok {
		t.Errorf("external link quay v1 was not added via AddReleaseLink; links=%v", rel.links)
	}
	// The link's URL must NEVER appear as an uploaded asset (it was not downloaded/re-hosted).
	if _, ok := rel.assets["quay v1"]; ok {
		t.Error("an external link was materialized as a file asset — it must be a link, not a download")
	}
}

// A per-asset failure is recorded in res.Errors but does NOT make ReconcileReleases return
// a fatal error, and does NOT stop the OTHER releases converging — the git mirror's
// success is independent of release-projection asset outcomes.
func TestReconcile_PerAssetErrorIsNonFatal(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	// A desired asset whose Source points at the src forge, but the src has NO such asset
	// → DownloadReleaseAsset errors during re-host.
	bad := DesiredRelease{
		Tag: "bad", Name: "bad", Body: "b",
		Assets: []DesiredAsset{{Name: "missing.bin", Source: forge.ReleaseAsset{Name: "missing.bin", URL: "fake://src/bad/missing.bin"}}},
	}
	good := DesiredRelease{Tag: "good", Name: "good", Body: "g"}

	res, err := ReconcileReleases(context.Background(), src, dst, []DesiredRelease{bad, good}, Options{})
	if err != nil {
		t.Fatalf("a per-asset error must NOT be fatal to the reconcile: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Error("expected the failed asset to be surfaced in res.Errors")
	}
	if dst.rels["good"] == nil {
		t.Error("the good release must still converge despite another release's asset error")
	}
}

// Unsupported-asset diagnostics ride through the reconcile onto the result — surfaced, not
// silently dropped by the fingerprint fast-path.
func TestReconcile_SurfacesDiagnostics(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	d := DesiredRelease{Tag: "v1", Name: "v1", Body: "n", Diagnostics: []string{"asset \"foo\": neither a file nor a link"}}
	res, err := ReconcileReleases(context.Background(), src, dst, []DesiredRelease{d}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0] != "asset \"foo\": neither a file nor a link" {
		t.Errorf("res.Diagnostics = %v, want the source diagnostic surfaced", res.Diagnostics)
	}
}
