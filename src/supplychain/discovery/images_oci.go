package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/credentials"
)

// ociProviders are the registry providers whose tag list is read over the OCI distribution
// API (GET /v2/<repo>/tags/list → {name, tags}, token-auth, Link pagination) rather than the
// Docker Hub web API. "docker" (Docker Hub) keeps its own richer web API path.
var ociProviders = map[string]bool{
	"harbor": true, "ghcr": true, "quay": true, "gitea": true, "generic": true,
}

// imageRegistry resolves which top-level registry StageFreight should read an image's tags
// from. A repo names it explicitly in dependency.registry (the single source of truth, by id —
// the same reference buildcache and publish use), which is what routes e.g. a ghcr.io base
// through a Harbor pull-through proxy. Returns nil when none is referenced, so the image falls
// back to the Docker Hub web API as before.
func (m *Resolver) imageRegistry() *config.RegistryConfig {
	if m.imageRegistryRef == "" {
		return nil
	}
	return config.FindRegistryByID(m.registries, m.imageRegistryRef)
}

// repoPath drops a leading registry host from an image reference, leaving the repository path
// the target registry expects: "ghcr.io/element-hq/synapse" → "element-hq/synapse", while
// "element-hq/synapse" and "library/nginx" pass through. A first segment is a host when it
// carries a dot or a port colon.
func repoPath(image string) string {
	parts := strings.SplitN(image, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:")) {
		return parts[1]
	}
	return image
}

// ociTags lists an image's tags over the OCI distribution API of the given registry, reusing
// that registry's own credentials (the <PREFIX>_USER/_TOKEN resolution shared with the build
// login) — no discovery-local token. go-containerregistry handles the token challenge and
// pagination. It returns the tags and the source URL, or an error the caller treats as a
// non-fatal registry miss.
func (m *Resolver) ociTags(ctx context.Context, reg *config.RegistryConfig, image string) ([]string, string, error) {
	host := strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(reg.URL, "https://"), "http://"), "/")
	ref := host + "/" + repoPath(image)
	repo, err := name.NewRepository(ref)
	if err != nil {
		return nil, "", fmt.Errorf("registry %s: %w", reg.ID, err)
	}

	auth := authn.Authenticator(authn.Anonymous)
	if reg.Credentials != "" {
		if c := credentials.ResolvePrefix(reg.Credentials); c.IsSet() {
			auth = &authn.Basic{Username: c.User, Password: c.Secret}
		}
	}

	tags, err := remote.List(repo, remote.WithContext(ctx), remote.WithAuth(auth))
	if err != nil {
		return nil, "", fmt.Errorf("registry %s: list %s: %w", reg.ID, ref, err)
	}
	return tags, "https://" + ref + "/tags/list", nil
}
