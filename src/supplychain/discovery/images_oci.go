package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"

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

// isDockerHub reports whether an image reference resolves to Docker Hub — no registry host, or
// an explicit docker.io / index.docker.io. go-containerregistry fills in the default registry
// when the reference carries no host, so a bare "golang" or "prplanit/x" is Docker Hub and a
// host-qualified "ghcr.io/owner/repo" is not.
func isDockerHub(image string) bool {
	repo, err := name.NewRepository(image)
	if err != nil {
		return true // let the Docker Hub namespace split handle an unparseable ref, as before
	}
	return repo.RegistryStr() == name.DefaultRegistry
}

// ociTagsCanonical lists an image's tags over its OWN registry's OCI distribution API,
// anonymously — the same client and path the upstream watcher uses. The reference already names
// its registry host, so this is the canonical enumeration surface: a pull-through proxy only
// knows the tags it has cached, so it can never be asked to list what is NEW upstream.
func ociTagsCanonical(ctx context.Context, image string) ([]string, string, error) {
	repo, err := name.NewRepository(image)
	if err != nil {
		return nil, "", err
	}
	tags, err := remote.List(repo, remote.WithContext(ctx), remote.WithAuth(authn.Anonymous))
	if err != nil {
		return nil, "", fmt.Errorf("list %s: %w", repo.Name(), err)
	}
	return tags, "https://" + repo.Name() + "/tags/list", nil
}

// ManifestDigestCanonical resolves the manifest digest ("sha256:…") of image:tag over the
// image's OWN registry's OCI distribution API, anonymously — the digest counterpart of
// ociTagsCanonical. A digest-pinned base bumped to a new tag must re-pin to THAT tag's digest
// from the canonical host; the Docker Hub manifest API (FetchManifestDigest's default) cannot
// answer for a ghcr.io/quay.io/lscr.io image.
func ManifestDigestCanonical(ctx context.Context, image, tag string) (string, error) {
	ref, err := name.NewTag(image + ":" + tag)
	if err != nil {
		return "", err
	}
	desc, err := remote.Head(ref, remote.WithContext(ctx), remote.WithAuth(authn.Anonymous))
	if err != nil {
		return "", fmt.Errorf("head %s: %w", ref.Name(), err)
	}
	return desc.Digest.String(), nil
}

// ManifestCreatedCanonical reads image:tag's creation time from the image's OWN registry,
// anonymously — the config's created field (org.opencontainers.image.created). For a
// track-governed base this is the tag's real publish time, the cooldown clock that frees the
// follow from StageFreight's first-sighting of the tag. A reproducible-build image zeroes this
// field (Unix epoch); the caller reads a zero or epoch time as "no date" and falls back to
// first-sighting.
func ManifestCreatedCanonical(ctx context.Context, image, tag string) (time.Time, error) {
	ref, err := name.NewTag(image + ":" + tag)
	if err != nil {
		return time.Time{}, err
	}
	img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithAuth(authn.Anonymous))
	if err != nil {
		return time.Time{}, fmt.Errorf("image %s: %w", ref.Name(), err)
	}
	cf, err := img.ConfigFile()
	if err != nil {
		return time.Time{}, fmt.Errorf("config %s: %w", ref.Name(), err)
	}
	return cf.Created.Time, nil
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
