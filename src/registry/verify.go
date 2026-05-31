package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// ImageVerifyTarget is one image reference to verify. Identity is the typed
// ArtifactID (the join key release_create uses to correlate results back to
// views) — never reconstructed from fields. Host/Path/Tag/Digest/CredentialRef
// are primitives obtained from a PublicationView; this struct stays
// backend-agnostic so the registry helpers don't import a domain-specific
// view type.
type ImageVerifyTarget struct {
	ArtifactID     artifact.ArtifactID
	Host           string
	Path           string
	Tag            string
	ExpectedDigest string // empty = don't enforce digest match
	CredentialRef  string
}

// VerificationResult tracks the outcome of verifying a single target.
// ArtifactID propagates from the input target so callers can join results
// back to their view collection by exact ArtifactID equality.
type VerificationResult struct {
	ArtifactID artifact.ArtifactID
	Host       string
	Path       string
	Tag        string
	Verified   bool
	Digest     string // remote digest if available
	Err        error
}

// VerifyImages checks each target against its remote registry. Uses OCI
// Distribution API HEAD (fallback GET) manifest request. Concurrent (max 8
// workers), retries with exponential backoff. Digest mismatch (when
// ExpectedDigest is non-empty) is a verification failure.
func VerifyImages(ctx context.Context, targets []ImageVerifyTarget, credResolver func(string) (string, string)) ([]VerificationResult, error) {
	results := make([]VerificationResult, len(targets))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for i, tgt := range targets {
		wg.Add(1)
		go func(idx int, tgt ImageVerifyTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := verifyOne(ctx, tgt, credResolver)
			results[idx] = result
		}(i, tgt)
	}

	wg.Wait()
	return results, nil
}

func verifyOne(ctx context.Context, tgt ImageVerifyTarget, credResolver func(string) (string, string)) VerificationResult {
	ref := tgt.Host + "/" + tgt.Path

	mkResult := func(verified bool, digest string, err error) VerificationResult {
		return VerificationResult{
			ArtifactID: tgt.ArtifactID,
			Host:       tgt.Host,
			Path:       tgt.Path,
			Tag:        tgt.Tag,
			Verified:   verified,
			Digest:     digest,
			Err:        err,
		}
	}

	// Retry with backoff: 1s, 2s, 3s, 4s, 5s
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return mkResult(false, "", ctx.Err())
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		digest, err := checkManifest(ctx, tgt.Host, tgt.Path, tgt.Tag, credResolver, tgt.CredentialRef)
		if err != nil {
			lastErr = err
			if isNotFound(err) {
				return mkResult(false, "", fmt.Errorf("image not found: %s:%s", ref, tgt.Tag))
			}
			continue
		}

		if tgt.ExpectedDigest != "" && digest != "" && tgt.ExpectedDigest != digest {
			return mkResult(false, digest,
				fmt.Errorf("digest mismatch for %s:%s: expected %s, remote %s", ref, tgt.Tag, tgt.ExpectedDigest, digest))
		}

		return mkResult(true, digest, nil)
	}

	return mkResult(false, "", fmt.Errorf("verification failed after retries: %w", lastErr))
}

// CheckManifestDigest performs a HEAD (fallback GET) on the OCI manifest endpoint.
// Returns the Docker-Content-Digest header value if available.
// Exported for cross-client digest verification (shadow write detection).
func CheckManifestDigest(ctx context.Context, host, path, tag string, credResolver func(string) (string, string), credRef string) (string, error) {
	return checkManifest(ctx, host, path, tag, credResolver, credRef)
}

// checkManifest performs a HEAD (fallback GET) on the OCI manifest endpoint.
// Returns the Docker-Content-Digest header value if available.
func checkManifest(ctx context.Context, host, path, tag string, credResolver func(string) (string, string), credRef string) (string, error) {
	url := fmt.Sprintf("https://%s/v2/%s/manifests/%s", host, path, tag)
	accept := "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json"

	// Try HEAD first
	digest, err := doManifestRequest(ctx, "HEAD", url, accept, host, credResolver, credRef)
	if err == nil {
		return digest, nil
	}

	// Fallback to GET if HEAD fails with unexpected error (not 401/404)
	if !isNotFound(err) && !isUnauthorized(err) {
		digest, err = doManifestRequest(ctx, "GET", url, accept, host, credResolver, credRef)
		if err == nil {
			return digest, nil
		}
	}

	return "", err
}

func doManifestRequest(ctx context.Context, method, url, accept, host string, credResolver func(string) (string, string), credRef string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", accept)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// Handle 401 — try token auth
	if resp.StatusCode == http.StatusUnauthorized {
		token, tokenErr := negotiateToken(ctx, resp, host, credResolver, credRef)
		if tokenErr != nil {
			return "", fmt.Errorf("auth negotiation failed: %w", tokenErr)
		}

		req2, _ := http.NewRequestWithContext(ctx, method, url, nil)
		req2.Header.Set("Accept", accept)
		req2.Header.Set("Authorization", "Bearer "+token)

		resp2, err2 := http.DefaultClient.Do(req2)
		if err2 != nil {
			return "", err2
		}
		defer func() {
			io.Copy(io.Discard, resp2.Body)
			resp2.Body.Close()
		}()

		if resp2.StatusCode == http.StatusNotFound {
			return "", &HTTPError{StatusCode: 404, Method: method, URL: url}
		}
		if resp2.StatusCode >= 400 {
			return "", &HTTPError{StatusCode: resp2.StatusCode, Method: method, URL: url}
		}
		return resp2.Header.Get("Docker-Content-Digest"), nil
	}

	if resp.StatusCode == http.StatusNotFound {
		return "", &HTTPError{StatusCode: 404, Method: method, URL: url}
	}
	if resp.StatusCode >= 400 {
		return "", &HTTPError{StatusCode: resp.StatusCode, Method: method, URL: url}
	}

	return resp.Header.Get("Docker-Content-Digest"), nil
}

// negotiateToken handles the OCI token auth flow using Www-Authenticate header.
func negotiateToken(ctx context.Context, resp *http.Response, host string, credResolver func(string) (string, string), credRef string) (string, error) {
	wwwAuth := resp.Header.Get("Www-Authenticate")
	if wwwAuth == "" {
		return "", fmt.Errorf("no Www-Authenticate header in 401 response")
	}

	// Parse "Bearer realm=...,service=...,scope=..."
	params := parseWWWAuthenticate(wwwAuth)
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("no realm in Www-Authenticate header")
	}

	tokenURL := realm
	sep := "?"
	if service := params["service"]; service != "" {
		tokenURL += sep + "service=" + service
		sep = "&"
	}
	if scope := params["scope"]; scope != "" {
		tokenURL += sep + "scope=" + scope
	}

	req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return "", err
	}

	// Add basic auth if credentials available
	if credResolver != nil && credRef != "" {
		user, pass := credResolver(credRef)
		if user != "" && pass != "" {
			req.SetBasicAuth(user, pass)
		}
	}

	tokenResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", tokenResp.StatusCode)
	}

	var tokenBody struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenBody); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}

	token := tokenBody.Token
	if token == "" {
		token = tokenBody.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("empty token in response")
	}

	return token, nil
}

// parseWWWAuthenticate parses a Bearer Www-Authenticate header into key-value pairs.
func parseWWWAuthenticate(header string) map[string]string {
	params := make(map[string]string)
	// Strip "Bearer " prefix
	header = strings.TrimPrefix(header, "Bearer ")
	header = strings.TrimPrefix(header, "bearer ")

	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		val := strings.TrimSpace(part[eq+1:])
		val = strings.Trim(val, `"`)
		params[key] = val
	}
	return params
}

func isNotFound(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 404
	}
	return false
}

func isUnauthorized(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 401
	}
	return false
}
