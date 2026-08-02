package registry

import (
	"strings"
	"testing"
)

func TestScopeDeniedMessage_GHCR(t *testing.T) {
	got := ScopeDeniedMessage("ghcr", "GHCR_TOKEN", 17)
	for _, want := range []string{
		"17 tags not pruned",
		"GHCR_TOKEN has insufficient scope",
		`prune requires "read:packages" "delete:packages"`,
		`(publish requires "write:packages")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message missing %q\n got: %s", want, got)
		}
	}
}

func TestScopeDeniedMessage_Singular(t *testing.T) {
	got := ScopeDeniedMessage("github", "tok", 1)
	if !strings.Contains(got, "1 tag not pruned") || strings.Contains(got, "1 tags") {
		t.Errorf("expected singular 'tag', got: %s", got)
	}
}

func TestScopeDeniedMessage_UnknownProvider(t *testing.T) {
	got := ScopeDeniedMessage("acme", "tok", 3)
	if strings.Contains(got, "prune requires") {
		t.Errorf("unknown provider must not claim specific scope names, got: %s", got)
	}
	if !strings.Contains(got, "lacks delete permission on acme") {
		t.Errorf("expected a generic message naming the provider, got: %s", got)
	}
}

func TestScopeDeniedMessage_EmptyCredential(t *testing.T) {
	got := ScopeDeniedMessage("ghcr", "", 2)
	if !strings.Contains(got, "the registry token") {
		t.Errorf("empty credential should fall back to a label, got: %s", got)
	}
}

func TestRequiredScopes_Known(t *testing.T) {
	if r := RequiredScopes("ghcr"); !r.Known {
		t.Error("ghcr should be Known")
	}
	if r := RequiredScopes("github"); !r.Known {
		t.Error("github should be Known")
	}
	if r := RequiredScopes("acme"); r.Known {
		t.Error("unknown provider should be !Known")
	}
}

func TestHTTPError_AbortsRetention(t *testing.T) {
	for code, want := range map[int]bool{401: true, 403: true, 404: false, 429: false, 500: false} {
		if got := (&HTTPError{StatusCode: code}).AbortsRetention(); got != want {
			t.Errorf("status %d: AbortsRetention = %v; want %v", code, got, want)
		}
	}
}
