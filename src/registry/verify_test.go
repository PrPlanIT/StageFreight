package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVerifyImageSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:abc123")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	targets := []ImageVerifyTarget{
		{ArtifactID: "docker:app", Host: host, Path: "org/app", Tag: "1.0.0"},
	}

	results, err := VerifyImages(context.Background(), targets, nil)
	if err != nil {
		t.Fatalf("VerifyImages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Verified {
		t.Fatalf("expected verified, got error: %v", results[0].Err)
	}
	if results[0].Digest != "sha256:abc123" {
		t.Fatalf("expected digest sha256:abc123, got %s", results[0].Digest)
	}
	if results[0].ArtifactID != "docker:app" {
		t.Fatalf("ArtifactID not propagated: got %q", results[0].ArtifactID)
	}
}

// TestVerifyImageStrictAcceptOCIManifest reproduces the v0.6.0 release failure:
// a strict registry (Harbor) 404s a stored manifest whose media type is absent
// from the Accept header. A single-arch OCI image manifest
// (application/vnd.oci.image.manifest.v1+json) must be accepted, or such images
// (which Docker Hub serves leniently) falsely verify as "image not found".
func TestVerifyImageStrictAcceptOCIManifest(t *testing.T) {
	const ociImageManifest = "application/vnd.oci.image.manifest.v1+json"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strict registry: only serve if the client said it accepts our type.
		if !strings.Contains(r.Header.Get("Accept"), ociImageManifest) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:ociimg")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	results, _ := VerifyImages(context.Background(),
		[]ImageVerifyTarget{{ArtifactID: "docker:app", Host: host, Path: "org/app", Tag: "1.0.0"}}, nil)
	if !results[0].Verified {
		t.Fatalf("single-arch OCI image manifest must verify against a strict-Accept registry; got: %v", results[0].Err)
	}

	// Guard the constant itself so the type can't silently drop out again.
	if !strings.Contains(manifestAcceptHeader, ociImageManifest) {
		t.Fatalf("manifestAcceptHeader must include %q", ociImageManifest)
	}
}

func TestVerifyImageNotFound(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	targets := []ImageVerifyTarget{
		{ArtifactID: "docker:app", Host: host, Path: "org/app", Tag: "1.0.0"},
	}

	results, _ := VerifyImages(context.Background(), targets, nil)
	if results[0].Verified {
		t.Fatal("expected not verified for 404")
	}
}

func TestVerifyImageRetrySuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:success")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	targets := []ImageVerifyTarget{
		{ArtifactID: "docker:app", Host: host, Path: "org/app", Tag: "1.0.0"},
	}

	results, _ := VerifyImages(context.Background(), targets, nil)
	if !results[0].Verified {
		t.Fatalf("expected verified after retry, got error: %v", results[0].Err)
	}
}

func TestVerifyImageDigestMismatch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:remote-different")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	targets := []ImageVerifyTarget{
		{ArtifactID: "docker:app", Host: host, Path: "org/app", Tag: "1.0.0", ExpectedDigest: "sha256:local-digest"},
	}

	results, _ := VerifyImages(context.Background(), targets, nil)
	if results[0].Verified {
		t.Fatal("expected not verified for digest mismatch")
	}
	if results[0].Err == nil {
		t.Fatal("expected error for digest mismatch")
	}
}
