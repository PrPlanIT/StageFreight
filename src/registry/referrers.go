package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// ArtifactLinks holds discovered OCI referrer artifact links for an image.
type ArtifactLinks struct {
	SBOM       string // digest ref for SBOM artifact (host/path@sha256:...)
	Provenance string // digest ref for provenance artifact
	Signature  string // digest ref for signature artifact
}

// Known artifact types for OCI referrers.
const (
	artifactSPDX       = "application/spdx+json"
	artifactCycloneDX  = "application/vnd.cyclonedx+json"
	artifactInToto     = "application/vnd.in-toto+json"
	artifactDSSE       = "application/vnd.dsse.envelope.v1+json"
	artifactCosign     = "application/vnd.dev.cosign.simplesigning.v1+json"
)

// referrerManifest represents a single referrer entry from the OCI referrers API.
type referrerManifest struct {
	MediaType    string `json:"mediaType"`
	Digest       string `json:"digest"`
	ArtifactType string `json:"artifactType"`
}

// referrersResponse is the OCI referrers API response.
type referrersResponse struct {
	Manifests []referrerManifest `json:"manifests"`
}

// DiscoverArtifacts queries the OCI referrers API for a verified image digest.
// Returns links to SBOM, provenance, and signature artifacts if present.
// Best-effort: returns empty ArtifactLinks (no error) if referrers API unsupported.
func DiscoverArtifacts(ctx context.Context, img build.PublishedImage, credResolver func(string) (string, string)) (ArtifactLinks, error) {
	if img.Digest == "" {
		return ArtifactLinks{}, nil
	}

	url := fmt.Sprintf("https://%s/v2/%s/referrers/%s", img.Host, img.Path, img.Digest)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ArtifactLinks{}, nil
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ArtifactLinks{}, nil // best-effort
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// Handle 401 — try token auth
	if resp.StatusCode == http.StatusUnauthorized {
		token, tokenErr := negotiateToken(ctx, resp, img.Host, credResolver, img.CredentialRef)
		if tokenErr != nil {
			return ArtifactLinks{}, nil // best-effort
		}

		req2, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		req2.Header.Set("Accept", "application/vnd.oci.image.index.v1+json")
		req2.Header.Set("Authorization", "Bearer "+token)

		resp2, err2 := http.DefaultClient.Do(req2)
		if err2 != nil {
			return ArtifactLinks{}, nil
		}
		defer func() {
			io.Copy(io.Discard, resp2.Body)
			resp2.Body.Close()
		}()

		if resp2.StatusCode != http.StatusOK {
			return ArtifactLinks{}, nil
		}

		return parseReferrers(resp2, img)
	}

	if resp.StatusCode != http.StatusOK {
		return ArtifactLinks{}, nil // unsupported or error — best-effort
	}

	return parseReferrers(resp, img)
}

func parseReferrers(resp *http.Response, img build.PublishedImage) (ArtifactLinks, error) {
	var rr referrersResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return ArtifactLinks{}, nil // parse failure is not fatal
	}

	var links ArtifactLinks
	imageBase := img.Host + "/" + img.Path

	for _, m := range rr.Manifests {
		ref := imageBase + "@" + m.Digest
		switch m.ArtifactType {
		case artifactSPDX:
			if links.SBOM == "" {
				links.SBOM = ref
			}
		case artifactCycloneDX:
			if links.SBOM == "" {
				links.SBOM = ref // SPDX preferred, CycloneDX fallback
			}
		case artifactInToto:
			if links.Provenance == "" {
				links.Provenance = ref
			}
		case artifactDSSE:
			if links.Provenance == "" {
				links.Provenance = ref // in-toto preferred, DSSE fallback
			}
		case artifactCosign:
			if links.Signature == "" {
				links.Signature = ref
			}
		}
	}

	return links, nil
}

// DiscoverAllArtifacts runs DiscoverArtifacts concurrently for multiple images.
// Deduplicates by host/path@digest to avoid redundant lookups.
func DiscoverAllArtifacts(ctx context.Context, images []build.PublishedImage, credResolver func(string) (string, string)) map[string]ArtifactLinks {
	type cacheKey struct{ hostPath, digest string }
	result := make(map[string]ArtifactLinks)
	var mu sync.Mutex

	// Dedup by host/path@digest
	seen := make(map[cacheKey]bool)
	var unique []build.PublishedImage
	for _, img := range images {
		if img.Digest == "" {
			continue
		}
		k := cacheKey{img.Host + "/" + img.Path, img.Digest}
		if seen[k] {
			continue
		}
		seen[k] = true
		unique = append(unique, img)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for _, img := range unique {
		wg.Add(1)
		go func(img build.PublishedImage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			links, _ := DiscoverArtifacts(ctx, img, credResolver)
			key := img.Host + "/" + img.Path + "@" + img.Digest

			mu.Lock()
			result[key] = links
			mu.Unlock()
		}(img)
	}

	wg.Wait()
	return result
}
