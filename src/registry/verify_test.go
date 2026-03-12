package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

func TestVerifyImageSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:abc123")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	// Override default client to accept test TLS
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	images := []build.PublishedImage{
		{Host: host, Path: "org/app", Tag: "1.0.0", Provider: "docker"},
	}

	results, err := VerifyImages(context.Background(), images, nil)
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

	images := []build.PublishedImage{
		{Host: host, Path: "org/app", Tag: "1.0.0", Provider: "docker"},
	}

	results, _ := VerifyImages(context.Background(), images, nil)
	if results[0].Verified {
		t.Fatal("expected not verified for 404")
	}
}

func TestVerifyImageRetrySuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n <= 2 {
			// Return 500 (retryable) for first 2 attempts
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

	images := []build.PublishedImage{
		{Host: host, Path: "org/app", Tag: "1.0.0", Provider: "docker"},
	}

	results, _ := VerifyImages(context.Background(), images, nil)
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

	images := []build.PublishedImage{
		{Host: host, Path: "org/app", Tag: "1.0.0", Provider: "docker", Digest: "sha256:local-digest"},
	}

	results, _ := VerifyImages(context.Background(), images, nil)
	if results[0].Verified {
		t.Fatal("expected not verified for digest mismatch")
	}
	if results[0].Err == nil {
		t.Fatal("expected error for digest mismatch")
	}
}
