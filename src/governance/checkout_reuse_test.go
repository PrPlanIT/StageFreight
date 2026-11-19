package governance

import (
	"os"
	"path/filepath"
	"testing"
)

// A repo is the unit of transfer; a preset is not. Seventeen presets from one policy
// repo must not mean seventeen clones — the cost would scale with how finely the policy
// is split, penalising the composition the preset system exists to encourage.
func TestCheckoutIsReusedPerRepoAndRef(t *testing.T) {
	ReleaseCheckouts()
	defer ReleaseCheckouts()

	clones := 0
	prev := fetchRepoFn
	fetchRepoFn = func(url, ref string) (string, error) {
		clones++
		return t.TempDir(), nil
	}
	defer func() { fetchRepoFn = prev }()

	for i := 0; i < 5; i++ {
		if _, err := checkout("https://example.org/Org/Policy", "main"); err != nil {
			t.Fatal(err)
		}
	}
	if clones != 1 {
		t.Errorf("cloned %d times for one repo+ref; want 1", clones)
	}

	// A different ref is a different tree, so it is fetched separately.
	if _, err := checkout("https://example.org/Org/Policy", "v2"); err != nil {
		t.Fatal(err)
	}
	if clones != 2 {
		t.Errorf("a second ref must be its own checkout; clones = %d", clones)
	}
}

func TestLoadGovernanceCheckoutIsReleased(t *testing.T) {
	ReleaseCheckouts()
	dir := t.TempDir() + "/policy"
	prev := fetchRepoFn
	fetchRepoFn = func(url, ref string) (string, error) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return dir, os.WriteFile(filepath.Join(dir, "governance.yml"), []byte("governance:\n  profiles: []\n"), 0o644)
	}
	defer func() { fetchRepoFn = prev }()

	if _, _, err := LoadGovernance(GovernanceSource{RepoURL: "https://example.org/Org/Policy", Path: "governance.yml"}); err != nil {
		t.Fatal(err)
	}
	if _, err := checkout("https://example.org/Org/Policy", "HEAD"); err != nil {
		t.Fatal(err)
	}
	ReleaseCheckouts()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("policy checkout survives ReleaseCheckouts: %v", err)
	}
}
