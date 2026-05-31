package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/artifact"
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
//
// Takes individual identity fields rather than a PublishedImage struct —
// this keeps the function free of v1 manifest coupling and aligns with the
// (target + digest) identity boundary used elsewhere.
func DiscoverArtifacts(ctx context.Context, host, path, digest, credentialRef string, credResolver func(string) (string, string)) (ArtifactLinks, error) {
	if digest == "" {
		return ArtifactLinks{}, nil
	}

	url := fmt.Sprintf("https://%s/v2/%s/referrers/%s", host, path, digest)

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
		token, tokenErr := negotiateToken(ctx, resp, host, credResolver, credentialRef)
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

		return parseReferrers(resp2, host, path)
	}

	if resp.StatusCode != http.StatusOK {
		return ArtifactLinks{}, nil // unsupported or error — best-effort
	}

	return parseReferrers(resp, host, path)
}

func parseReferrers(resp *http.Response, host, path string) (ArtifactLinks, error) {
	var rr referrersResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return ArtifactLinks{}, nil // parse failure is not fatal
	}

	var links ArtifactLinks
	imageBase := host + "/" + path

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

// ArtifactDiscoveryTarget is one (artifact, registry coordinate, digest)
// tuple to look up referrers for. Mirrors ImageVerifyTarget's identity
// model — ArtifactID is the typed join key, primitives carry the registry
// coordinates and credential reference.
type ArtifactDiscoveryTarget struct {
	ArtifactID    artifact.ArtifactID
	Host          string
	Path          string
	Digest        string
	CredentialRef string
}

// DiscoverAllArtifacts runs DiscoverArtifacts concurrently for the supplied
// targets. Deduplicates registry queries by (Host, Path, Digest) tuple
// (presentation grouping, not identity), but the returned map is keyed by
// the typed ArtifactID so consumers join back via exact ArtifactID
// equality.
//
// If multiple targets share the same (Host, Path, Digest) — common when
// the same image is published under multiple tags — they all map to the
// same ArtifactLinks result with one underlying registry call.
func DiscoverAllArtifacts(ctx context.Context, targets []ArtifactDiscoveryTarget, credResolver func(string) (string, string)) map[artifact.ArtifactID]ArtifactLinks {
	result := make(map[artifact.ArtifactID]ArtifactLinks)
	var mu sync.Mutex

	// Presentation-only dedup key for the registry query. Not an identity.
	type queryKey struct {
		hostPath string
		digest   string
	}
	type queryPlan struct {
		host          string
		path          string
		digest        string
		credentialRef string
		mappedIDs     []artifact.ArtifactID
	}
	planByQuery := make(map[queryKey]*queryPlan)
	for _, tgt := range targets {
		if tgt.Digest == "" {
			continue
		}
		k := queryKey{tgt.Host + "/" + tgt.Path, tgt.Digest}
		plan, exists := planByQuery[k]
		if !exists {
			plan = &queryPlan{
				host:          tgt.Host,
				path:          tgt.Path,
				digest:        tgt.Digest,
				credentialRef: tgt.CredentialRef,
			}
			planByQuery[k] = plan
		}
		plan.mappedIDs = append(plan.mappedIDs, tgt.ArtifactID)
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, plan := range planByQuery {
		wg.Add(1)
		go func(p *queryPlan) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			links, _ := DiscoverArtifacts(ctx, p.host, p.path, p.digest, p.credentialRef, credResolver)
			mu.Lock()
			for _, id := range p.mappedIDs {
				result[id] = links
			}
			mu.Unlock()
		}(plan)
	}
	wg.Wait()
	return result
}
