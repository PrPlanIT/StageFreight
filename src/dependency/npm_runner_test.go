package dependency

import (
	"os"
	"strings"
	"testing"
)

func TestHardenedNpmArgs(t *testing.T) {
	got := hardenedNpmArgs([]string{"install", "left-pad@1.3.0", "--package-lock-only"})
	if len(got) == 0 || got[0] != "--ignore-scripts" {
		t.Fatalf("--ignore-scripts must be first and unconditional, got %v", got)
	}
}

func TestHardenedNpmEnv_ScrubsSecrets(t *testing.T) {
	os.Setenv("NPM_TOKEN", "supersecret")
	os.Setenv("GITHUB_TOKEN", "ghp_secret")
	defer os.Unsetenv("NPM_TOKEN")
	defer os.Unsetenv("GITHUB_TOKEN")

	env := hardenedNpmEnv("/opt/node/bin")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "supersecret") || strings.Contains(joined, "ghp_secret") || strings.Contains(joined, "NPM_TOKEN") {
		t.Errorf("hardened env leaked a secret:\n%s", joined)
	}
	if !strings.Contains(joined, "PATH=/opt/node/bin") {
		t.Errorf("PATH must be pinned to the node bin dir, got:\n%s", joined)
	}
}
