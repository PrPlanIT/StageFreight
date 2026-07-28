package mirror

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/forge"
)

// ── in-memory fake forge (satisfies the narrow releaseForge interface) ──────

type fakeAsset struct {
	id, name, digest string
	size             int64
	data             []byte
}
type fakeRelease struct {
	id, tag, name, body string
	prerelease          bool
	assets              map[string]*fakeAsset
}
type fakeForge struct {
	name     string
	rels     map[string]*fakeRelease // by tag
	seq      int
	uploads  []string // "<tag>/<asset>" per UploadAsset — drift assertions
	deletedA []string // "<tag>/<asset>" per DeleteReleaseAsset
}

func newFake(name string) *fakeForge { return &fakeForge{name: name, rels: map[string]*fakeRelease{}} }

func digestOf(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

func (f *fakeForge) findByID(id string) *fakeRelease {
	for _, r := range f.rels {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (f *fakeForge) ListReleases(context.Context) ([]forge.ReleaseInfo, error) {
	var out []forge.ReleaseInfo
	for _, r := range f.rels {
		out = append(out, forge.ReleaseInfo{ID: r.id, TagName: r.tag, Name: r.name, Description: r.body, Prerelease: r.prerelease})
	}
	return out, nil
}
func (f *fakeForge) CreateRelease(_ context.Context, o forge.ReleaseOptions) (*forge.Release, error) {
	f.seq++
	id := fmt.Sprintf("%s-%d", f.name, f.seq)
	f.rels[o.TagName] = &fakeRelease{id: id, tag: o.TagName, name: o.Name, body: o.Description,
		prerelease: o.Type == forge.ReleaseTypePrerelease, assets: map[string]*fakeAsset{}}
	return &forge.Release{ID: id}, nil
}
func (f *fakeForge) DeleteRelease(_ context.Context, tag string) error {
	delete(f.rels, tag)
	return nil
}
func (f *fakeForge) UpdateReleaseNotes(_ context.Context, id, body string) error {
	if r := f.findByID(id); r != nil {
		r.body = body
		return nil
	}
	return fmt.Errorf("no release %s", id)
}
func (f *fakeForge) UploadAsset(_ context.Context, id string, a forge.Asset) error {
	r := f.findByID(id)
	if r == nil {
		return fmt.Errorf("no release %s", id)
	}
	data, err := os.ReadFile(a.FilePath)
	if err != nil {
		return err
	}
	f.seq++
	r.assets[a.Name] = &fakeAsset{id: fmt.Sprintf("a-%d", f.seq), name: a.Name, digest: digestOf(data), size: int64(len(data)), data: data}
	f.uploads = append(f.uploads, r.tag+"/"+a.Name)
	return nil
}
func (f *fakeForge) ListReleaseAssets(_ context.Context, id string) ([]forge.ReleaseAsset, error) {
	r := f.findByID(id)
	if r == nil {
		return nil, nil
	}
	var out []forge.ReleaseAsset
	for _, a := range r.assets {
		out = append(out, forge.ReleaseAsset{ID: a.id, Name: a.name, Size: a.size, Digest: a.digest,
			URL: fmt.Sprintf("fake://%s/%s/%s", f.name, r.tag, a.name)})
	}
	return out, nil
}
func (f *fakeForge) DeleteReleaseAsset(_ context.Context, id, assetID string) error {
	r := f.findByID(id)
	if r == nil {
		return nil
	}
	for n, a := range r.assets {
		if a.id == assetID {
			delete(r.assets, n)
			f.deletedA = append(f.deletedA, r.tag+"/"+n)
		}
	}
	return nil
}
func (f *fakeForge) DownloadReleaseAsset(_ context.Context, a forge.ReleaseAsset) (io.ReadCloser, error) {
	// URL: fake://<forge>/<tag>/<name>
	for _, r := range f.rels {
		if fa, ok := r.assets[a.Name]; ok && a.URL == fmt.Sprintf("fake://%s/%s/%s", f.name, r.tag, a.Name) {
			return io.NopCloser(bytes.NewReader(fa.data)), nil
		}
	}
	return nil, fmt.Errorf("asset not found: %s", a.URL)
}

// helper: stand up a source release with assets, return the DesiredRelease for it.
func srcRelease(src *fakeForge, tag, body string, assets map[string][]byte) DesiredRelease {
	src.CreateRelease(context.Background(), forge.ReleaseOptions{TagName: tag, Name: tag, Description: body})
	r := src.rels[tag]
	d := DesiredRelease{Tag: tag, Name: tag, Body: body}
	for n, data := range assets {
		src.seq++
		fa := &fakeAsset{id: fmt.Sprintf("s-%d", src.seq), name: n, digest: digestOf(data), size: int64(len(data)), data: data}
		r.assets[n] = fa
		d.Assets = append(d.Assets, DesiredAsset{Name: n, Digest: fa.digest, Size: fa.size,
			Source: forge.ReleaseAsset{Name: n, Digest: fa.digest, Size: fa.size, URL: fmt.Sprintf("fake://%s/%s/%s", src.name, tag, n)}})
	}
	return d
}

func reconcile(t *testing.T, src, dst *fakeForge, desired []DesiredRelease, opts Options) *Result {
	t.Helper()
	res, err := ReconcileReleases(context.Background(), src, dst, desired, opts)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, e := range res.Errors {
		t.Fatalf("reconcile error: %v", e)
	}
	return res
}

// ── scenarios ───────────────────────────────────────────────────────────

// Create-missing: a desired release lands on the mirror with its binary + marker.
func TestReconcile_CreatesMissing(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	d := srcRelease(src, "v1.0.0", "notes", map[string][]byte{"app.tar.gz": []byte("BINARY")})

	res := reconcile(t, src, dst, []DesiredRelease{d}, Options{})
	if len(res.Created) != 1 {
		t.Fatalf("created=%v", res.Created)
	}
	m := dst.rels["v1.0.0"]
	if m == nil || m.assets["app.tar.gz"] == nil {
		t.Fatal("mirror release/asset missing")
	}
	if string(m.assets["app.tar.gz"].data) != "BINARY" {
		t.Fatal("binary not re-hosted")
	}
	if _, ours := readMarker(m.body); !ours {
		t.Fatal("provenance marker not stamped")
	}
}

// Idempotent: a second pass with no change is a fingerprint no-op — zero uploads.
func TestReconcile_Idempotent(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	d := srcRelease(src, "v1.0.0", "notes", map[string][]byte{"a": []byte("A"), "b": []byte("B")})

	reconcile(t, src, dst, []DesiredRelease{d}, Options{})
	dst.uploads = nil // reset
	res := reconcile(t, src, dst, []DesiredRelease{d}, Options{})
	if res.InSync != 1 || len(res.Updated) != 0 {
		t.Fatalf("expected in-sync no-op, got InSync=%d Updated=%v", res.InSync, res.Updated)
	}
	if len(dst.uploads) != 0 {
		t.Fatalf("no-op run re-uploaded: %v", dst.uploads)
	}
}

// Granular drift: only the changed asset is re-hosted, not the whole release.
func TestReconcile_GranularAssetDrift(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	d := srcRelease(src, "v1.0.0", "notes", map[string][]byte{"a": []byte("A"), "b": []byte("B")})
	reconcile(t, src, dst, []DesiredRelease{d}, Options{})

	// "b" is re-signed on the source (new content → new digest → new fingerprint).
	src.rels["v1.0.0"].assets["b"].data = []byte("B2")
	src.rels["v1.0.0"].assets["b"].digest = digestOf([]byte("B2"))
	d2 := srcRelease2(src, "v1.0.0") // rebuild desired from current source state
	dst.uploads, dst.deletedA = nil, nil

	res := reconcile(t, src, dst, []DesiredRelease{d2}, Options{})
	if len(res.Updated) != 1 {
		t.Fatalf("expected update, got %+v", res)
	}
	if len(dst.uploads) != 1 || dst.uploads[0] != "v1.0.0/b" {
		t.Fatalf("expected only 'b' re-hosted, got uploads=%v", dst.uploads)
	}
	if string(dst.rels["v1.0.0"].assets["b"].data) != "B2" {
		t.Fatal("drifted asset not refreshed")
	}
}

// Foreign untouched: a human-authored mirror release (no marker) with a desired
// tag is NEVER modified — and NEVER pruned.
func TestReconcile_ForeignUntouched(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	// human made this on the mirror directly — no SF marker
	dst.CreateRelease(context.Background(), forge.ReleaseOptions{TagName: "v1.0.0", Name: "human", Description: "HAND-WRITTEN"})
	d := srcRelease(src, "v1.0.0", "sf notes", map[string][]byte{"a": []byte("A")})

	res := reconcile(t, src, dst, []DesiredRelease{d}, Options{Prune: true})
	if len(res.SkippedForeign) != 1 {
		t.Fatalf("expected foreign skip, got %+v", res)
	}
	if dst.rels["v1.0.0"].body != "HAND-WRITTEN" {
		t.Fatal("clobbered a human release")
	}
	if len(dst.rels["v1.0.0"].assets) != 0 {
		t.Fatal("uploaded into a foreign release")
	}
}

// Prune is provenance-bounded: our stale release is pruned; a foreign one is not.
func TestReconcile_PruneProvenanceBounded(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	// two releases SF created earlier, plus a contributor's own
	old := srcRelease(src, "dev-old", "notes", map[string][]byte{"a": []byte("A")})
	cur := srcRelease(src, "dev-new", "notes", map[string][]byte{"a": []byte("A")})
	reconcile(t, src, dst, []DesiredRelease{old, cur}, Options{}) // both now ours on mirror
	dst.CreateRelease(context.Background(), forge.ReleaseOptions{TagName: "contrib-1.0", Description: "COMMUNITY"})

	// retention drops dev-old from the desired set; contributor release is foreign
	res := reconcile(t, src, dst, []DesiredRelease{cur}, Options{Prune: true})
	if len(res.Pruned) != 1 || res.Pruned[0] != "dev-old" {
		t.Fatalf("expected only dev-old pruned, got %v", res.Pruned)
	}
	if dst.rels["contrib-1.0"] == nil {
		t.Fatal("pruned a foreign (community) release — UNACCEPTABLE")
	}
	if dst.rels["dev-new"] == nil {
		t.Fatal("pruned the current release")
	}
}

// Prune off (additive): our stale release survives; nothing is deleted.
func TestReconcile_AdditiveKeepsExtras(t *testing.T) {
	src, dst := newFake("src"), newFake("dst")
	old := srcRelease(src, "dev-old", "notes", map[string][]byte{"a": []byte("A")})
	cur := srcRelease(src, "dev-new", "notes", map[string][]byte{"a": []byte("A")})
	reconcile(t, src, dst, []DesiredRelease{old, cur}, Options{})

	res := reconcile(t, src, dst, []DesiredRelease{cur}, Options{Prune: false})
	if len(res.Pruned) != 0 {
		t.Fatalf("additive must not prune, got %v", res.Pruned)
	}
	if dst.rels["dev-old"] == nil {
		t.Fatal("additive dropped an extra")
	}
}

// rebuild a DesiredRelease from the source fake's CURRENT state (post-mutation).
func srcRelease2(src *fakeForge, tag string) DesiredRelease {
	r := src.rels[tag]
	d := DesiredRelease{Tag: tag, Name: r.name, Body: r.body}
	for n, a := range r.assets {
		d.Assets = append(d.Assets, DesiredAsset{Name: n, Digest: a.digest, Size: a.size,
			Source: forge.ReleaseAsset{Name: n, Digest: a.digest, Size: a.size, URL: fmt.Sprintf("fake://%s/%s/%s", src.name, tag, n)}})
	}
	return d
}
