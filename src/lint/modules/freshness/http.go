package freshness

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// userAgent identifies StageFreight to registries. crates.io (and others) reject a
// missing or default ("Go-http-client/1.1") User-Agent with 403 — a documented,
// enforced policy — which silently rendered every cargo dependency "unresolved".
const userAgent = "StageFreight (+https://github.com/PrPlanIT/StageFreight)"

// httpClient wraps a standard http.Client with convenience helpers.
type httpClient struct {
	client *http.Client
}

// newHTTPClient creates a client with the given timeout in seconds.
func newHTTPClient(timeoutSecs int) *httpClient {
	if timeoutSecs <= 0 {
		timeoutSecs = 10
	}
	return &httpClient{
		client: &http.Client{
			Timeout: time.Duration(timeoutSecs) * time.Second,
		},
	}
}

// fetchJSON GETs a URL and decodes the response body into result.
// If ep is non-nil and has an AuthEnv, the resolved token is sent
// as a Bearer header.
func (h *httpClient) fetchJSON(ctx context.Context, url string, result any, ep ...*RegistryEndpoint) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("freshness: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	applyAuth(req, ep...)

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("freshness: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("freshness: GET %s: status %d", url, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("freshness: decode %s: %w", url, err)
	}
	return nil
}

// fetchBytes GETs a URL and returns the raw response body.
func (h *httpClient) fetchBytes(ctx context.Context, url string, ep ...*RegistryEndpoint) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("freshness: create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	applyAuth(req, ep...)

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("freshness: GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("freshness: GET %s: status %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("freshness: read %s: %w", url, err)
	}
	return data, nil
}

// headDigest issues a HEAD request and returns the Docker-Content-Digest header.
func (h *httpClient) headDigest(ctx context.Context, url string, accept string, ep ...*RegistryEndpoint) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", fmt.Errorf("freshness: create request: %w", err)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", userAgent)
	applyAuth(req, ep...)

	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("freshness: HEAD %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("freshness: HEAD %s: status %d", url, resp.StatusCode)
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("freshness: HEAD %s: no Docker-Content-Digest header", url)
	}
	return digest, nil
}

// applyAuth sets a Bearer token from the RegistryEndpoint's AuthEnv.
func applyAuth(req *http.Request, ep ...*RegistryEndpoint) {
	if len(ep) == 0 || ep[0] == nil {
		return
	}
	envName := ep[0].AuthEnv
	if envName == "" {
		return
	}
	token := os.Getenv(envName)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}
