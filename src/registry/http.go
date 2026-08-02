package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// HTTPError represents an HTTP response with a non-success status code.
type HTTPError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// httpClient is a thin wrapper for JSON API calls used by all providers.
type httpClient struct {
	base    string // base URL (e.g., "https://hub.docker.com")
	headers map[string]string
}

// doJSON executes an HTTP request with optional JSON body and decodes the response.
func (c *httpClient) doJSON(ctx context.Context, method, url string, body interface{}, result interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return resp, &HTTPError{StatusCode: resp.StatusCode, Method: method, URL: url, Body: truncateBody(respBody, 512)}
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return resp, fmt.Errorf("decoding response from %s %s: %w", method, url, err)
		}
	}
	return resp, nil
}

// doRaw executes an HTTP request and returns the raw response. Caller must close body.
func (c *httpClient) doRaw(ctx context.Context, method, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &HTTPError{StatusCode: resp.StatusCode, Method: method, URL: url, Body: truncateBody(body, 512)}
	}

	return resp, nil
}

func truncateBody(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// IsForbidden returns true if the error chain contains an HTTP 403 response.
func IsForbidden(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 403
	}
	return false
}

// AbortsRetention reports whether this error should stop a retention run rather
// than be retried per-tag. A 401/403 is credential-wide — every remaining delete
// would fail identically — so the retention engine records the rest as blocked
// instead of issuing N doomed calls. (Satisfies the engine's aborter interface
// structurally, without the retention package importing registry.)
func (e *HTTPError) AbortsRetention() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}
