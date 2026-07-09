package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// FetchManifestDigest queries the registry v2 manifest endpoint for the
// digest of image:tag. Used by the freshness renderer's digest-lock check
// for non-versioned tags (e.g. "latest", "noble").
func (m *Resolver) FetchManifestDigest(ctx context.Context, image, tag string) (string, error) {
	namespace, repo := SplitImageNamespace(image)
	ep := m.cfg.registryEndpoint(supplychain.EcosystemDockerImage)
	defaultURL := fmt.Sprintf("https://registry.hub.docker.com/v2/repositories/%s/%s/tags/%s", namespace, repo, tag)
	url := m.cfg.registryURL(supplychain.EcosystemDockerImage, defaultURL)
	if url != defaultURL {
		// Custom registry: use v2 manifests endpoint.
		url = strings.TrimRight(url, "/") + fmt.Sprintf("/%s/%s/manifests/%s", namespace, repo, tag)
	}

	var resp struct {
		Digest string `json:"digest"`
	}
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return "", err
	}
	if resp.Digest == "" {
		return "", fmt.Errorf("no digest for %s/%s:%s", namespace, repo, tag)
	}
	return resp.Digest, nil
}
