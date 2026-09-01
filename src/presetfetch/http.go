package presetfetch

import (
	"fmt"
	"io"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/PrPlanIT/StageFreight/src/presetref"
)

const (
	// userAgent identifies StageFreight to preset hosts.
	userAgent = "StageFreight-Preset/1"
	// maxPresetBytes bounds an untrusted response. A preset is a small text document;
	// this is a guard against a misaddressed URL streaming something enormous, not a
	// policy limit on legitimate presets.
	maxPresetBytes = 4 << 20
)

// newHTTPFetcher returns the fetcher for http(s) preset sources. Its own client rather
// than a shared one: this retrieves a small text document, so it wants a short timeout,
// unlike the toolchain downloader (large archives, minutes) or the registry API client
// (h2 keep-alive hardening, retries).
func newHTTPFetcher() presetref.Fetcher {
	return &httpFetcher{client: &http.Client{Timeout: 30 * time.Second}}
}

type httpFetcher struct{ client *http.Client }

// Fetch retrieves the document the URL denotes. The URL is the entire source, so ref
// and path are empty (Parse guarantees it) and are accepted only to satisfy the
// interface.
func (h *httpFetcher) Fetch(source, _, _ string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("preset URL %q: %w", source, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching preset %q: %w", source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetching preset %q: HTTP %d", source, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPresetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading preset %q: %w", source, err)
	}
	if len(body) > maxPresetBytes {
		return nil, fmt.Errorf("preset %q exceeds %d bytes", source, maxPresetBytes)
	}
	// A preset is a text document. Saying so here names the real problem, instead of
	// surfacing it later as an unintelligible YAML parse error.
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("preset %q is not text (invalid UTF-8)", source)
	}
	return body, nil
}

// Classify is Tracked for every URL, as a consequence of URLs having no revision
// semantics: the reference denotes whatever the URL currently serves, with the retained
// response as the fallback. Operators wanting immutability address a revision-bearing
// source instead.
func (h *httpFetcher) Classify(_, _, _ string) (presetref.Kind, error) {
	return presetref.Tracked, nil
}

// Revision asks the server whether the document changed, without downloading it. An
// ETag is the strong answer; Last-Modified the weak one. A server offering neither
// returns "" and the caller fetches, which is correct rather than merely conservative:
// with no validator, "unchanged" is not something we can claim.
func (h *httpFetcher) Revision(source, _, _ string) (string, error) {
	req, err := http.NewRequest(http.MethodHead, source, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := h.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("HEAD %s: HTTP %d", source, resp.StatusCode)
	}
	if tag := resp.Header.Get("ETag"); tag != "" {
		return tag, nil
	}
	return resp.Header.Get("Last-Modified"), nil
}
