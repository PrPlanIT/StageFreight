package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

func TestDiscoverArtifactsSBOM(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := referrersResponse{
			Manifests: []referrerManifest{
				{MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: "sha256:sbom123", ArtifactType: artifactSPDX},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	img := artifact.PublishedImage{Host: host, Path: "org/app", Tag: "1.0.0", Digest: "sha256:imgdigest"}
	links, err := DiscoverArtifacts(context.Background(), img, nil)
	if err != nil {
		t.Fatalf("DiscoverArtifacts: %v", err)
	}
	expected := host + "/org/app@sha256:sbom123"
	if links.SBOM != expected {
		t.Fatalf("expected SBOM=%s, got %s", expected, links.SBOM)
	}
}

func TestDiscoverArtifactsMultiple(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := referrersResponse{
			Manifests: []referrerManifest{
				{Digest: "sha256:sbom1", ArtifactType: artifactSPDX},
				{Digest: "sha256:prov1", ArtifactType: artifactInToto},
				{Digest: "sha256:sig1", ArtifactType: artifactCosign},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	img := artifact.PublishedImage{Host: host, Path: "org/app", Tag: "1.0.0", Digest: "sha256:imgdigest"}
	links, _ := DiscoverArtifacts(context.Background(), img, nil)

	if links.SBOM == "" {
		t.Fatal("expected SBOM link")
	}
	if links.Provenance == "" {
		t.Fatal("expected Provenance link")
	}
	if links.Signature == "" {
		t.Fatal("expected Signature link")
	}
}

func TestDiscoverArtifactsNoReferrers(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := referrersResponse{Manifests: []referrerManifest{}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	img := artifact.PublishedImage{Host: host, Path: "org/app", Tag: "1.0.0", Digest: "sha256:imgdigest"}
	links, _ := DiscoverArtifacts(context.Background(), img, nil)

	if links.SBOM != "" || links.Provenance != "" || links.Signature != "" {
		t.Fatal("expected empty links for no referrers")
	}
}

func TestDiscoverArtifactsUnsupportedRegistry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	origClient := http.DefaultClient
	http.DefaultClient = srv.Client()
	defer func() { http.DefaultClient = origClient }()

	img := artifact.PublishedImage{Host: host, Path: "org/app", Tag: "1.0.0", Digest: "sha256:imgdigest"}
	links, err := DiscoverArtifacts(context.Background(), img, nil)
	if err != nil {
		t.Fatalf("expected no error for unsupported registry, got %v", err)
	}
	if links.SBOM != "" || links.Provenance != "" || links.Signature != "" {
		t.Fatal("expected empty links for unsupported registry")
	}
}

func TestDiscoverArtifactsNoDigest(t *testing.T) {
	img := artifact.PublishedImage{Host: "docker.io", Path: "org/app", Tag: "1.0.0"}
	links, err := DiscoverArtifacts(context.Background(), img, nil)
	if err != nil {
		t.Fatalf("expected no error for no digest, got %v", err)
	}
	if links.SBOM != "" || links.Provenance != "" || links.Signature != "" {
		t.Fatal("expected empty links for no digest")
	}
}
