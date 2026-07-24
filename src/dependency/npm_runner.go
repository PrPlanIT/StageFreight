package dependency

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// nodeToolRunner runs node-bundled package managers (npm, and yarn/pnpm via corepack)
// from a checksum-verified Node.js tree, HARDENED against Shai-Hulud-class supply-chain
// worms on every invocation: lifecycle scripts disabled, secrets scrubbed. StageFreight
// only re-resolves a lockfile — it never installs project code or exposes a credential.
type nodeToolRunner struct {
	binDir string // <node-tree>/bin — holds node, npm, corepack
}

// resolveNodeTools provisions node (which bundles npm + corepack) and returns a runner.
func resolveNodeTools(repoRoot string) (*nodeToolRunner, error) {
	res, err := toolchain.Resolve(repoRoot, "node", "")
	if err != nil {
		return nil, fmt.Errorf("node toolchain: %w", err)
	}
	return &nodeToolRunner{binDir: filepath.Dir(res.Path)}, nil
}

// run executes `tool args...` in dir with the secret-scrubbed env (node on PATH) plus
// any extraEnv (e.g. YARN_ENABLE_SCRIPTS=false).
func (n *nodeToolRunner) run(ctx context.Context, dir, tool string, extraEnv []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, filepath.Join(n.binDir, tool), args...)
	cmd.Dir = dir
	cmd.Env = append(hardenedNpmEnv(n.binDir), extraEnv...)
	return cmd.CombinedOutput()
}

// syncLock regenerates whichever lockfile is present in dir (npm / yarn / pnpm), scripts
// disabled, and returns the touched lockfile relative to repoRoot ("" if none).
func (n *nodeToolRunner) syncLock(ctx context.Context, repoRoot, dir string) (string, error) {
	tool, args, extraEnv, lockfile, ok := nodeLockCommand(dir)
	if !ok {
		return "", nil // no recognized lockfile — the manifest edit stands alone
	}
	if out, err := n.run(ctx, dir, tool, extraEnv, args...); err != nil {
		rel, _ := filepath.Rel(repoRoot, dir)
		return "", fmt.Errorf("%s lock sync in %s: %w\n%s", tool, rel, err, strings.TrimSpace(string(out)))
	}
	rel, _ := filepath.Rel(repoRoot, filepath.Join(dir, lockfile))
	return rel, nil
}

// nodeLockCommand inspects dir and returns the HARDENED lock-regeneration command for
// whichever lockfile is present, or ok=false if none. Pure (no exec) so it is unit-
// testable. Each command re-resolves the lock WITHOUT installing (no node_modules) and
// with scripts disabled:
//   - pnpm:  corepack pnpm install --lockfile-only --ignore-scripts
//   - yarn:  corepack yarn install --mode=update-lockfile   (installs nothing; belt-and-
//     suspenders YARN_ENABLE_SCRIPTS=false)
//   - npm:   npm install --package-lock-only --ignore-scripts
func nodeLockCommand(dir string) (tool string, args, extraEnv []string, lockfile string, ok bool) {
	switch {
	case fileExists(filepath.Join(dir, "pnpm-lock.yaml")):
		return "corepack", []string{"pnpm", "install", "--lockfile-only", "--ignore-scripts"}, nil, "pnpm-lock.yaml", true
	case fileExists(filepath.Join(dir, "yarn.lock")):
		return "corepack", []string{"yarn", "install", "--mode=update-lockfile"}, []string{"YARN_ENABLE_SCRIPTS=false"}, "yarn.lock", true
	case fileExists(filepath.Join(dir, "package-lock.json")):
		return "npm", hardenedNpmArgs([]string{"install", "--package-lock-only"}), nil, "package-lock.json", true
	}
	return "", nil, nil, "", false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hardenedNpmArgs prepends --ignore-scripts to an npm invocation. First + unconditional:
// no caller can opt out of disabling lifecycle scripts.
func hardenedNpmArgs(args []string) []string {
	return append([]string{"--ignore-scripts"}, args...)
}

// hardenedNpmEnv returns the package manager's environment: the toolchain's scrubbed base
// (no secrets — CleanEnv forwards only HOME/PATH/proxy) with PATH pinned to the provisioned
// node bin dir. NPM_TOKEN / GITHUB_TOKEN / AWS_* etc. are absent by construction.
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
