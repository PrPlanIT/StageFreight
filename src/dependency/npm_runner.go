package dependency

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// npmRunner executes an npm subcommand in a directory. Every invocation is HARDENED
// (see resolveNpmRunner): lifecycle scripts disabled, secrets scrubbed.
type npmRunner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// resolveNpmRunner provisions a checksum-verified Node.js/npm and returns a runner that
// is hardened against Shai-Hulud-class npm supply-chain worms:
//
//   - --ignore-scripts on EVERY invocation → lifecycle scripts (pre/post-install), the
//     primary remote-code-execution vector, never run. StageFreight only re-resolves a
//     lockfile; it never needs a package's scripts.
//   - a secret-scrubbed environment (toolchain.CleanEnv) → no NPM_TOKEN / GITHUB_TOKEN /
//     cloud credentials reach npm, so even a hypothetical execution has nothing to steal
//     or to self-propagate with.
//   - node reachable only via the provisioned tree (PATH pinned to its bin dir).
//
// (min_release_age cooldown, applied at the policy layer, additionally dodges the window
// in which a freshly-compromised version is briefly live before it is yanked.)
func resolveNpmRunner(repoRoot string) (npmRunner, error) {
	res, err := toolchain.Resolve(repoRoot, "node", "")
	if err != nil {
		return nil, fmt.Errorf("node toolchain: %w", err)
	}
	binDir := filepath.Dir(res.Path) // <tree>/bin — holds node + npm (symlink)
	npm := filepath.Join(binDir, "npm")
	return func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, npm, hardenedNpmArgs(args)...)
		cmd.Dir = dir
		cmd.Env = hardenedNpmEnv(binDir)
		return cmd.CombinedOutput()
	}, nil
}

// hardenedNpmArgs prepends --ignore-scripts to every npm invocation. It is first and
// unconditional: no caller can opt out of disabling lifecycle scripts.
func hardenedNpmArgs(args []string) []string {
	return append([]string{"--ignore-scripts"}, args...)
}

// hardenedNpmEnv returns npm's environment: the toolchain's scrubbed base (no secrets)
// with PATH pinned to the provisioned node bin dir (so npm finds its own node, nothing
// else). NPM_TOKEN / GITHUB_TOKEN / AWS_* etc. are absent by construction — CleanEnv
// forwards only HOME/PATH/proxy.
func hardenedNpmEnv(nodeBinDir string) []string {
	env := toolchain.CleanEnv()
	replaced := false
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			env[i] = "PATH=" + nodeBinDir
			replaced = true
		}
	}
	if !replaced {
		env = append(env, "PATH="+nodeBinDir)
	}
	return env
}
