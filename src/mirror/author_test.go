package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/forge"
)

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func localAsset(t *testing.T, name string, data []byte) DesiredAsset {
	p := writeTemp(t, name, data)
	return DesiredAsset{Name: name, Local: p, Digest: digestOf(data), Size: int64(len(data))}
}

// Upsert: create, then a re-run is a no-op, then a changed local file updates
// only that asset. (The `sf release create` idempotency + fix-a-binary path.)
func TestUpsert_CreateNoopUpdate(t *testing.T) {
	ctx := context.Background()
	dst := newFake("dst")
	d := DesiredRelease{Tag: "v1.0.0", Name: "v1.0.0", Body: "notes",
		Assets: []DesiredAsset{localAsset(t, "app", []byte("v1"))}}

	res, err := UpsertReleases(ctx, dst, []DesiredRelease{d})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 1 {
		t.Fatalf("create=%v", res.Created)
	}
	if string(dst.rels["v1.0.0"].assets["app"].data) != "v1" {
		t.Fatal("asset not authored from local file")
	}

	// re-run, unchanged → fingerprint no-op, zero uploads
	dst.uploads = nil
	res2, _ := UpsertReleases(ctx, dst, []DesiredRelease{d})
	if res2.InSync != 1 || len(dst.uploads) != 0 {
		t.Fatalf("re-run must be a no-op, got InSync=%d uploads=%v", res2.InSync, dst.uploads)
	}

	// fix the binary → new local file → only that asset is replaced
	d.Assets[0] = localAsset(t, "app", []byte("v2-fixed"))
	dst.uploads = nil
	res3, _ := UpsertReleases(ctx, dst, []DesiredRelease{d})
	if len(res3.Updated) != 1 || len(dst.uploads) != 1 {
		t.Fatalf("fix should update one asset, got %+v uploads=%v", res3, dst.uploads)
	}
	if string(dst.rels["v1.0.0"].assets["app"].data) != "v2-fixed" {
		t.Fatal("binary not fixed on the forge")
	}
}

// Destroy removes a release by tag across every named forge.
func TestDestroy_AcrossForges(t *testing.T) {
	ctx := context.Background()
	a, b := newFake("a"), newFake("b")
	a.CreateRelease(ctx, forge.ReleaseOptions{TagName: "v0.9-leaked"})
	b.CreateRelease(ctx, forge.ReleaseOptions{TagName: "v0.9-leaked"})

	if errs := DestroyRelease(ctx, "v0.9-leaked", a, b); len(errs) != 0 {
		t.Fatalf("destroy errors: %v", errs)
	}
	if a.rels["v0.9-leaked"] != nil || b.rels["v0.9-leaked"] != nil {
		t.Fatal("release not destroyed on every forge")
	}
}
