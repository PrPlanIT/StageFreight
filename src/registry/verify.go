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

	"github.com/PrPlanIT/StageFreight/src/build"
)

// VerificationResult tracks the outcome of verifying a single published image.
type VerificationResult struct {
	Image    build.PublishedImage
	Verified bool
	Digest   string // remote digest if available
	Err      error
}

// VerifyImages checks each published image against its remote registry.
// Uses OCI Distribution API HEAD (fallback GET) manifest request.
// Concurrent (max 8 workers), retries with exponential backoff.
// Digest mismatch is a verification failure.
func VerifyImages(ctx context.Context, images []build.PublishedImage, credResolver func(string) (string, string)) ([]VerificationResult, error) {
	results := make([]VerificationResult, len(images))

	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for i, img := range images {
		wg.Add(1)
		go func(idx int, img build.PublishedImage) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result := verifyImage(ctx, img, credResolver)
			results[idx] = result
		}(i, img)
	}

	wg.Wait()
	return results, nil
}

func verifyImage(ctx context.Context, img build.PublishedImage, credResolver func(string) (string, string)) VerificationResult {
	ref := img.Host + "/" + img.Path
	tag := img.Tag

	// Retry with backoff: 1s, 2s, 3s, 4s, 5s
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return VerificationResult{Image: img, Err: ctx.Err()}
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		digest, err := checkManifest(ctx, img.Host, img.Path, tag, credResolver, img.CredentialRef)
		if err != nil {
			lastErr = err
			// Don't retry on 404 — image genuinely not found
			if isNotFound(err) {
				return VerificationResult{Image: img, Err: fmt.Errorf("image not found: %s:%s", ref, tag)}
			}
			continue
		}

		// Digest mismatch check
		if img.Digest != "" && digest != "" && img.Digest != digest {
			return VerificationResult{
				Image:  img,
				Digest: digest,
				Err:    fmt.Errorf("digest mismatch for %s:%s: local %s, remote %s", ref, tag, img.Digest, digest),
			}
		}

		return VerificationResult{Image: img, Verified: true, Digest: digest}
	}

	return VerificationResult{Image: img, Err: fmt.Errorf("verification failed after retries: %w", lastErr)}
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
