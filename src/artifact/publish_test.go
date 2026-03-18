package artifact

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "docker.io", Path: "prplanit/stagefreight", Tag: "1.0.0", Provider: "docker", Digest: "sha256:abc123"},
			{Host: "ghcr.io", Path: "prplanit/stagefreight", Tag: "1.0.0", Provider: "github"},
		},
	}

	if err := WritePublishManifest(dir, manifest); err != nil {
		t.Fatalf("WritePublishManifest: %v", err)
	}

	got, err := ReadPublishManifest(dir)
	if err != nil {
		t.Fatalf("ReadPublishManifest: %v", err)
	}

	if len(got.Published) != 2 {
		t.Fatalf("expected 2 images, got %d", len(got.Published))
	}
	if got.Published[0].Host != "docker.io" {
		t.Fatalf("expected docker.io, got %s", got.Published[0].Host)
	}
	if got.Published[0].Ref != "docker.io/prplanit/stagefreight:1.0.0" {
		t.Fatalf("expected canonical ref, got %s", got.Published[0].Ref)
	}
	if got.Timestamp == "" {
		t.Fatal("expected timestamp to be set")
	}
}

func TestPublishManifestChecksumMismatchFails(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "docker.io", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker"},
		},
	}

	if err := WritePublishManifest(dir, manifest); err != nil {
		t.Fatalf("WritePublishManifest: %v", err)
	}

	// Tamper with the manifest JSON
	manifestPath := filepath.Join(dir, PublishManifestPath)
	data, _ := os.ReadFile(manifestPath)
	data = append(data, []byte("tampered")...)
	os.WriteFile(manifestPath, data, 0o644)

	_, err := ReadPublishManifest(dir)
	if !errors.Is(err, ErrPublishManifestInvalid) {
		t.Fatalf("expected ErrPublishManifestInvalid, got %v", err)
	}
}

func TestPublishManifestMissingChecksumFails(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "docker.io", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker"},
		},
	}

	if err := WritePublishManifest(dir, manifest); err != nil {
		t.Fatalf("WritePublishManifest: %v", err)
	}

	// Remove checksum file
	os.Remove(filepath.Join(dir, PublishManifestPath+".sha256"))

	_, err := ReadPublishManifest(dir)
	if !errors.Is(err, ErrPublishManifestInvalid) {
		t.Fatalf("expected ErrPublishManifestInvalid, got %v", err)
	}
}

func TestPublishManifestDedup(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "docker.io", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker", Digest: "sha256:abc"},
			{Host: "docker.io", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker", Digest: "sha256:abc"},
		},
	}

	if err := WritePublishManifest(dir, manifest); err != nil {
		t.Fatalf("WritePublishManifest: %v", err)
	}

	got, err := ReadPublishManifest(dir)
	if err != nil {
		t.Fatalf("ReadPublishManifest: %v", err)
	}
	if len(got.Published) != 1 {
		t.Fatalf("expected 1 deduped image, got %d", len(got.Published))
	}
}

func TestPublishManifestConflictingDigest(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "docker.io", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker", Digest: "sha256:abc"},
			{Host: "docker.io", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker", Digest: "sha256:def"},
		},
	}

	err := WritePublishManifest(dir, manifest)
	if err == nil {
		t.Fatal("expected error for conflicting digests")
	}
}

func TestPublishManifestDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "ghcr.io", Path: "org/app", Tag: "2.0.0", Provider: "github"},
			{Host: "docker.io", Path: "org/app", Tag: "1.0.0", Provider: "docker"},
			{Host: "docker.io", Path: "org/app", Tag: "0.5.0", Provider: "docker"},
			{Host: "ghcr.io", Path: "org/app", Tag: "1.0.0", Provider: "github"},
		},
	}

	if err := WritePublishManifest(dir, manifest); err != nil {
		t.Fatalf("WritePublishManifest: %v", err)
	}

	got, err := ReadPublishManifest(dir)
	if err != nil {
		t.Fatalf("ReadPublishManifest: %v", err)
	}

	expected := []string{
		"docker.io/org/app:0.5.0",
		"docker.io/org/app:1.0.0",
		"ghcr.io/org/app:1.0.0",
		"ghcr.io/org/app:2.0.0",
	}
	for i, img := range got.Published {
		if img.Ref != expected[i] {
			t.Fatalf("index %d: expected %s, got %s", i, expected[i], img.Ref)
		}
	}
}

func TestReadPublishManifestNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadPublishManifest(dir)
	if !errors.Is(err, ErrPublishManifestNotFound) {
		t.Fatalf("expected ErrPublishManifestNotFound, got %v", err)
	}
}

func TestWritePublishManifestCanonicalizesRef(t *testing.T) {
	dir := t.TempDir()
	manifest := PublishManifest{
		Published: []PublishedImage{
			{Host: "https://Docker.IO/", Path: "prplanit/app", Tag: "1.0.0", Provider: "docker", Ref: "wrong-ref"},
		},
	}

	if err := WritePublishManifest(dir, manifest); err != nil {
		t.Fatalf("WritePublishManifest: %v", err)
	}

	got, err := ReadPublishManifest(dir)
	if err != nil {
		t.Fatalf("ReadPublishManifest: %v", err)
	}

	if got.Published[0].Ref != "docker.io/prplanit/app:1.0.0" {
		t.Fatalf("expected canonicalized ref, got %s", got.Published[0].Ref)
	}
	if got.Published[0].Host != "docker.io" {
		t.Fatalf("expected normalized host, got %s", got.Published[0].Host)
	}
}
