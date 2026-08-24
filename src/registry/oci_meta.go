package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OCITagMeta is the per-tag metadata read from an OCI registry's manifest + config blob.
// It is the registry-agnostic shape behind {registry.<id>.tag.<tag>.*} tokens for every
// provider that is not Docker Hub (ghcr, harbor, quay, generic OCI) — the size that Docker
// Hub serves from its own API is computed here from the manifest instead.
type OCITagMeta struct {
	Size    int64     // total compressed size: image config blob + all layer blobs
	Digest  string    // the tag's manifest digest (Docker-Content-Digest header)
	Created time.Time // image build time, from the config blob's .created
}

// ociConfigAccept is the Accept for an image config blob (both OCI and legacy docker types).
const ociConfigAccept = "application/vnd.oci.image.config.v1+json, " +
	"application/vnd.docker.container.image.v1+json, */*"

// FetchOCITagInfo fetches per-tag metadata for any OCI-compliant registry (ghcr.io, harbor,
// quay, …) via the /v2 manifest + blob API with the standard OCI token-auth flow. Public
// images resolve anonymously — negotiateToken requests an anonymous pull token when no
// credentials are available — so a badge fetch needs no PAT for public packages; credRef
// supplies basic auth for private ones. Best-effort per tag: a tag that errors is omitted
// from the result (its token resolves to empty, which the badge layer renders as n/a).
func FetchOCITagInfo(ctx context.Context, host, path string, tags []string, credResolver func(string) (string, string), credRef string) map[string]OCITagMeta {
	host = ociHost(host)
	out := make(map[string]OCITagMeta, len(tags))
	for _, tag := range tags {
		if m, err := fetchOneOCITag(ctx, host, path, tag, credResolver, credRef); err == nil {
			out[tag] = m
		}
	}
	return out
}

// ociHost strips any scheme/trailing slash so config URLs like "ghcr.io" and
// "https://cr.pcfae.com/" both yield a bare host for /v2 URL construction.
func ociHost(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	return strings.TrimSuffix(h, "/")
}

// ociManifest is the subset of an image manifest OR index we read: config+layers for a
// concrete manifest, manifests[] for a multi-arch index.
type ociManifest struct {
	Config struct {
		Size   int64  `json:"size"`
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Size int64 `json:"size"`
	} `json:"layers"`
	Manifests []struct {
		Digest   string `json:"digest"`
		Platform struct {
			OS           string `json:"os"`
			Architecture string `json:"architecture"`
		} `json:"platform"`
	} `json:"manifests"`
}

// isIndex reports whether this parsed body is a multi-arch index (manifest list) rather than
// a concrete image manifest.
func (m ociManifest) isIndex() bool { return len(m.Manifests) > 0 }

// totalSize sums a concrete manifest's compressed size: the config blob plus every layer.
func (m ociManifest) totalSize() int64 {
	total := m.Config.Size
	for _, l := range m.Layers {
		total += l.Size
	}
	return total
}

// pickPlatformDigest chooses which sub-manifest of an index to measure, preferring
// linux/amd64 and falling back to the first entry.
func (m ociManifest) pickPlatformDigest() string {
	if len(m.Manifests) == 0 {
		return ""
	}
	for _, sub := range m.Manifests {
		if sub.Platform.OS == "linux" && sub.Platform.Architecture == "amd64" {
			return sub.Digest
		}
	}
	return m.Manifests[0].Digest
}

func fetchOneOCITag(ctx context.Context, host, path, ref string, credResolver func(string) (string, string), credRef string) (OCITagMeta, error) {
	var meta OCITagMeta

	manifestURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, path, ref)
	body, digest, err := ociGet(ctx, host, manifestURL, manifestAcceptHeader, credResolver, credRef)
	if err != nil {
		return meta, err
	}
	meta.Digest = digest // the tag's digest (index digest for multi-arch — the right one to show)

	var m ociManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return meta, err
	}

	// Multi-arch index: resolve to a concrete platform manifest (prefer linux/amd64) so the
	// size reflects a real image rather than the index's tiny descriptor list.
	if m.isIndex() {
		subURL := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, path, m.pickPlatformDigest())
		subBody, _, serr := ociGet(ctx, host, subURL, manifestAcceptHeader, credResolver, credRef)
		if serr != nil {
			return meta, serr
		}
		m = ociManifest{}
		if err := json.Unmarshal(subBody, &m); err != nil {
			return meta, err
		}
	}

	meta.Size = m.totalSize()

	// The image config blob carries .created — the build time, used as the tag's "updated".
	if m.Config.Digest != "" {
		blobURL := fmt.Sprintf("https://%s/v2/%s/blobs/%s", host, path, m.Config.Digest)
		if cfgBody, _, cerr := ociGet(ctx, host, blobURL, ociConfigAccept, credResolver, credRef); cerr == nil {
			var cfg struct {
				Created string `json:"created"`
			}
			if json.Unmarshal(cfgBody, &cfg) == nil && cfg.Created != "" {
				if t, perr := time.Parse(time.RFC3339, cfg.Created); perr == nil {
					meta.Created = t
				}
			}
		}
	}

	return meta, nil
}

// ociGet performs a GET with the OCI token-auth retry (401 → negotiateToken → Bearer),
// returning the response body and the Docker-Content-Digest header. It is the body-returning
// sibling of doManifestRequest (which returns only the digest), sharing negotiateToken so a
// public image is fetched anonymously and a private one via credRef's basic auth.
func ociGet(ctx context.Context, host, url, accept string, credResolver func(string) (string, string), credRef string) ([]byte, string, error) {
	do := func(bearer string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		return http.DefaultClient.Do(req)
	}

	resp, err := do("")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		token, terr := negotiateToken(ctx, resp, host, credResolver, credRef)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if terr != nil {
			return nil, "", terr
		}
		if resp, err = do(token); err != nil {
			return nil, "", err
		}
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		return nil, "", &HTTPError{StatusCode: resp.StatusCode, Method: "GET", URL: url}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB cap — manifests/configs are small
	if err != nil {
		return nil, "", err
	}
	return body, resp.Header.Get("Docker-Content-Digest"), nil
}
