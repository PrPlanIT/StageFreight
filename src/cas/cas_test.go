package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLayout writes a minimal but VALID OCI layout directory: the manifest
// blob `blob` is stored under blobs/sha256/<hash>, and index.json references
// that digest in its manifest list. Returns (layoutDir, digest) where digest is
// the manifest blob's sha256. This mirrors a real buildx OCI layout closely
// enough to exercise both verification checks (identity binding via index.json,
// content integrity via blob rehash) without a docker build.
func writeLayout(t *testing.T, blob []byte) (string, Digest) {
	t.Helper()
	sum := sha256.Sum256(blob)
	hexHash := hex.EncodeToString(sum[:])
	dir := t.TempDir()
	blobDir := filepath.Join(dir, "blobs", "sha256")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		t.Fatalf("mkdir blobs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blobDir, hexHash), blob, 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write oci-layout: %v", err)
	}
	index := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:%s","size":%d}]}`, hexHash, len(blob))
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatalf("write index.json: %v", err)
	}
	return dir, Digest("sha256:" + hexHash)
}

func TestFSStore_RoundTrip(t *testing.T) {
	src, digest := writeLayout(t, []byte("manifest-bytes-A"))
	store := NewFSStore(t.TempDir())

	stored, err := store.Put(digest, src)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored == "" {
		t.Fatal("Put returned empty stored dir")
	}

	resolved, err := store.Resolve(digest)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The resolved blob must exist and re-hash to the key (Resolve already
	// verified; assert the file is really there for good measure).
	if _, err := os.Stat(filepath.Join(resolved, "blobs", "sha256", string(digest)[len("sha256:"):])); err != nil {
		t.Fatalf("resolved layout missing named blob: %v", err)
	}
}

func TestFSStore_ResolveUnknownIsLoud(t *testing.T) {
	store := NewFSStore(t.TempDir())
	const absent = Digest("sha256:" + "0000000000000000000000000000000000000000000000000000000000000000")

	_, err := store.Resolve(absent)
	if err == nil {
		t.Fatal("Resolve of unknown digest returned nil error — must be loud, never empty success")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestFSStore_TamperDetectedOnResolve(t *testing.T) {
	src, digest := writeLayout(t, []byte("manifest-bytes-B"))
	store := NewFSStore(t.TempDir())

	stored, err := store.Put(digest, src)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Corrupt the stored blob in place — simulate bit-rot / tampering.
	blobPath := filepath.Join(stored, "blobs", "sha256", string(digest)[len("sha256:"):])
	if err := os.WriteFile(blobPath, []byte("tampered"), 0o644); err != nil {
		t.Fatalf("corrupt blob: %v", err)
	}

	_, err = store.Resolve(digest)
	if err == nil {
		t.Fatal("Resolve returned corrupt bytes without error — verify-on-read failed")
	}
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("want ErrIntegrity, got %v", err)
	}
}

func TestFSStore_PutRejectsLayoutNotMatchingDigest(t *testing.T) {
	src, _ := writeLayout(t, []byte("real-bytes"))
	store := NewFSStore(t.TempDir())

	// A digest that does NOT name any blob in the layout.
	const wrong = Digest("sha256:" + "1111111111111111111111111111111111111111111111111111111111111111")
	_, err := store.Put(wrong, src)
	if err == nil {
		t.Fatal("Put accepted a layout whose digest names no blob — must reject at write time")
	}
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("want ErrIntegrity, got %v", err)
	}
}

func TestFSStore_Idempotent(t *testing.T) {
	src, digest := writeLayout(t, []byte("manifest-bytes-C"))
	store := NewFSStore(t.TempDir())

	first, err := store.Put(digest, src)
	if err != nil {
		t.Fatalf("Put #1: %v", err)
	}
	second, err := store.Put(digest, src)
	if err != nil {
		t.Fatalf("Put #2 (idempotent): %v", err)
	}
	if first != second {
		t.Fatalf("idempotent Put returned different dirs: %q vs %q", first, second)
	}
}

func TestNoopStore_RequiresNoExportAndRetainsNothing(t *testing.T) {
	var s Store = NewNoopStore()
	if s.RequiresOCIExport() {
		t.Error("NoopStore.RequiresOCIExport() = true, want false (nothing retained → no export)")
	}
	digest := Digest("sha256:" + strings.Repeat("a", 64))
	stored, err := s.Put(digest, "/nonexistent")
	if err != nil {
		t.Errorf("NoopStore.Put should not error (persistence-disabled is valid): %v", err)
	}
	if stored != "" {
		t.Errorf("NoopStore.Put stored dir = %q, want empty", stored)
	}
	if _, err := s.Resolve(digest); err == nil {
		t.Error("NoopStore.Resolve returned nil error — must be a loud miss")
	}
}

func TestFSStore_RequiresOCIExport(t *testing.T) {
	var s Store = NewFSStore(t.TempDir())
	if !s.RequiresOCIExport() {
		t.Error("FSStore.RequiresOCIExport() = false, want true")
	}
}

func TestSplitDigest_RejectsBadInput(t *testing.T) {
	bad := []Digest{
		"",
		"sha256:",
		":abc",
		"md5:" + "0000000000000000000000000000000000000000000000000000000000000000",
		"sha256:tooshort",
		"noprefix",
	}
	for _, d := range bad {
		if _, _, err := splitDigest(d); err == nil {
			t.Errorf("splitDigest(%q) = nil error, want rejection", d)
		}
	}
}
