package forge

import (
	"context"
	"errors"
	"testing"
)

// TestAzureDevOpsImplementsForge: the compile-time assertion is the real test —
// AzureDevOpsForge must satisfy the full Forge interface. The runtime checks pin
// identity and the honest "no native releases" behavior.
func TestAzureDevOpsImplementsForge(t *testing.T) {
	a := NewAzureDevOps("https://dev.azure.com/myorg/")

	var _ Forge = a // all 15 methods present, or this file won't compile

	if a.Provider() != AzureDevOps {
		t.Fatalf("Provider() = %q, want %q", a.Provider(), AzureDevOps)
	}
	if a.BaseURL != "https://dev.azure.com/myorg" {
		t.Fatalf("BaseURL = %q (trailing slash should be trimmed)", a.BaseURL)
	}

	// Azure DevOps has no native git-release object — these must be unsupported,
	// not silently faked.
	if _, err := a.CreateRelease(context.Background(), ReleaseOptions{}); !errors.Is(err, ErrNotSupported) {
		t.Errorf("CreateRelease should return ErrNotSupported, got %v", err)
	}
	if _, err := a.ListReleases(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Errorf("ListReleases should return ErrNotSupported, got %v", err)
	}
}

// TestDetectProviderAzureDevOps: dev.azure.com and *.visualstudio.com resolve to
// Azure DevOps without disturbing the other providers.
func TestDetectProviderAzureDevOps(t *testing.T) {
	cases := map[string]Provider{
		"https://dev.azure.com/org/proj/_git/repo": AzureDevOps,
		"git@ssh.dev.azure.com:v3/org/proj/repo":   AzureDevOps,
		"https://org.visualstudio.com/proj/_git/r": AzureDevOps,
		"https://github.com/org/repo.git":          GitHub,
		"https://codeberg.org/org/repo.git":        Forgejo,
	}
	for url, want := range cases {
		if got := DetectProvider(url); got != want {
			t.Errorf("DetectProvider(%q) = %q, want %q", url, got, want)
		}
	}
}
