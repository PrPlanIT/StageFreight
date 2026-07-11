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

func TestNodeLockCommand(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) { os.WriteFile(dir+"/"+name, []byte("x"), 0644) }
	rm := func(name string) { os.Remove(dir + "/" + name) }

	// none
	if _, _, _, _, ok := nodeLockCommand(dir); ok {
		t.Error("no lockfile → ok=false")
	}
	// npm
	write("package-lock.json")
	if tool, args, _, lf, ok := nodeLockCommand(dir); !ok || tool != "npm" || lf != "package-lock.json" || args[0] != "--ignore-scripts" {
		t.Errorf("npm: tool=%q args=%v lf=%q ok=%v", tool, args, lf, ok)
	}
	// pnpm wins over npm (pnpm-lock checked first)
	write("pnpm-lock.yaml")
	if tool, args, _, lf, ok := nodeLockCommand(dir); !ok || tool != "corepack" || lf != "pnpm-lock.yaml" || !contains(args, "--ignore-scripts") || !contains(args, "--lockfile-only") {
		t.Errorf("pnpm: tool=%q args=%v lf=%q", tool, args, lf)
	}
	rm("pnpm-lock.yaml")
	// yarn
	write("yarn.lock")
	if tool, args, extra, lf, ok := nodeLockCommand(dir); !ok || tool != "corepack" || lf != "yarn.lock" || !contains(args, "--mode=update-lockfile") || !contains(extra, "YARN_ENABLE_SCRIPTS=false") {
		t.Errorf("yarn: tool=%q args=%v extra=%v lf=%q", tool, args, extra, lf)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
