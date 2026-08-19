package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/http2"
)

// userAgent identifies StageFreight to registries. crates.io (and others) reject a
// missing or default ("Go-http-client/1.1") User-Agent with 403 — a documented,
// enforced policy — which silently rendered every cargo dependency "unresolved".
const userAgent = "StageFreight (+https://github.com/PrPlanIT/StageFreight)"

// httpClient wraps a standard http.Client with convenience helpers.
type httpClient struct {
	client  *http.Client
	timeout time.Duration // per-request deadline, applied via context on retried calls
}

// newHTTPClient creates a client with the given timeout in seconds.
//
// The transport is hardened against a specific, catastrophic failure mode: a dead
// HTTP/2 keep-alive. When a peer (e.g. api.osv.dev behind a load balancer) stops
// answering WITHOUT closing the TCP connection — a black-hole — a multiplexed h2
// stream wedges in roundTrip indefinitely, and http.Client.Timeout does NOT reliably
// break it. ReadIdleTimeout makes the h2 transport send health-check PINGs on an idle
// connection and evict it when PingTimeout passes with no reply, so a black-holed
// connection is detected and discarded instead of silently poisoning every request
// that follows. Per-request context deadlines (see doJSONRetry) are the hard per-call
// bound layered on top — together they make a stalled endpoint recoverable rather than
// a whole-pipeline hang.
func newHTTPClient(timeoutSecs int) *httpClient {
	if timeoutSecs <= 0 {
		timeoutSecs = 10
	}
	timeout := time.Duration(timeoutSecs) * time.Second

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if t2, err := http2.ConfigureTransports(transport); err == nil && t2 != nil {
		t2.ReadIdleTimeout = 15 * time.Second
		t2.PingTimeout = 5 * time.Second
	}

	return &httpClient{
		client: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		timeout: timeout,
	}
}

// isRetryableStatus reports whether an HTTP status warrants a retry: 429 (rate limit)
// and 5xx (transient server/proxy failure). A definitive 4xx is the server's real
// answer and is never retried.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

// doJSONRetry performs a JSON request (GET, or POST with reqBody) with a per-ATTEMPT
// context deadline and bounded exponential backoff, decoding a 200 body into result.
//
// Each attempt is individually capped at h.timeout via context.WithTimeout — the bound
// that http2 roundTrip honors even when Client.Timeout does not — so no single attempt
// can hang. Transient conditions (network error, timeout, 429, 5xx) are retried up to
// maxAttempts; a definitive 4xx or a JSON decode error is returned immediately (the
// server answered, retrying changes nothing). Cancellation of the PARENT context stops
// the loop at once. The returned error is non-nil only when every attempt was exhausted
// — the caller treats that as UNVERIFIED, never as a clean result.
func (h *httpClient) doJSONRetry(ctx context.Context, method, url string, reqBody, result any) error {
	var payload []byte
	if reqBody != nil {
		var err error
		if payload, err = json.Marshal(reqBody); err != nil {
			return err
		}
	}

	const maxAttempts = 3
	backoff := 500 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, h.timeout)
		transient, err := h.doJSONOnce(attemptCtx, method, url, payload, result)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil { // parent cancelled/expired — stop trying
			return fmt.Errorf("%s %s: %w", method, url, ctx.Err())
		}
		if !transient || attempt == maxAttempts {
			return fmt.Errorf("%s %s: %w", method, url, lastErr)
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return fmt.Errorf("%s %s: %w", method, url, ctx.Err())
		}
		backoff *= 2
	}
	return lastErr
}

// doJSONOnce performs a single JSON request attempt. It reports whether the failure
// (if any) is transient so doJSONRetry can decide to retry.
func (h *httpClient) doJSONOnce(ctx context.Context, method, url string, payload []byte, result any) (transient bool, err error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return true, err // network error / timeout — transient
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return isRetryableStatus(resp.StatusCode), fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return false, fmt.Errorf("decode: %w", err) // server answered; body is just bad
	}
	return false, nil
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
