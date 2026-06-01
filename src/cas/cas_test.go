package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeLayout writes a minimal OCI-layout-shaped directory whose single blob
// has content `blob` and returns (layoutDir, digest) where digest is the real
// sha256 of blob. This lets the tests exercise the rehash invariant
// (sha256(blobs/sha256/<X>) == X) without a docker build.
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
	// minimal index.json + oci-layout marker, so the stored thing looks like a
	// real layout (not strictly needed for the rehash check, but realistic).
	if err := os.WriteFile(filepath.Join(dir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		t.Fatalf("write oci-layout: %v", err)
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
