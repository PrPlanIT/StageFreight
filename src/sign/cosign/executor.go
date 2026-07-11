package cosign

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// The executors are the only place cosign is actually run. Each renders the
// trust+policy flags from the plan (Render), appends the op target, and execs
// cosign with a class-appropriate environment (signEnv). They carry NO trust
// logic of their own — that lives entirely in Render. Orchestration (when/whether
// to sign, ordering vs distribution, recording outcomes) belongs in Publish.

// SignImage signs an image digest ref (repo@sha256:…; tags are never used) per the
// plan's trust contract. opts.MultiArch adds --recursive (sign the whole index).
func SignImage(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, digestRef string, plan sign.SignPlan, env Env, opts sign.SignOptions) error {
	args, err := Render(plan, sign.OpSignImage, env)
	if err != nil {
		return fmt.Errorf("cosign sign: %w", err)
	}
	if opts.MultiArch {
		args = append(args, "--recursive")
	}
	args = append(args, digestRef)
	return run(ctx, rootDir, desired, plan, "sign", args)
}

// Attest binds a predicate to an image digest ref per the plan. The predicate
// path AND type are caller-supplied (opts) — the executor serializes them, it
// never defaults a predicate semantic of its own.
func Attest(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, digestRef string, plan sign.SignPlan, env Env, opts sign.SignOptions) error {
	if opts.PredicatePath == "" || opts.PredicateType == "" {
		return fmt.Errorf("cosign attest: predicate path and type are required (the executor never defaults them)")
	}
	args, err := Render(plan, sign.OpAttest, env)
	if err != nil {
		return fmt.Errorf("cosign attest: %w", err)
	}
	args = append(args, "--predicate", opts.PredicatePath, "--type", opts.PredicateType, digestRef)
	return run(ctx, rootDir, desired, plan, "attest", args)
}

// SignBlob signs a detached blob (e.g. SHA256SUMS) per the plan, writing the
// detached signature to sigPath. The caller chooses sigPath so additive,
// multi-tier signing can write distinct files (never clobbering a lower-tier sig).
func SignBlob(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, blobPath, sigPath string, plan sign.SignPlan, env Env) error {
	args, err := Render(plan, sign.OpSignBlob, env)
	if err != nil {
		return fmt.Errorf("cosign sign-blob: %w", err)
	}
	args = append(args, "--output-signature", sigPath, blobPath)
	return run(ctx, rootDir, desired, plan, "sign-blob", args)
}

// Available reports whether cosign can be resolved via the toolchain.
func Available(rootDir string, desired map[string]config.ToolConstraint) bool {
	ver, _ := toolchain.ResolveVersion(rootDir, "cosign", "", desired)
	_, err := toolchain.Resolve(rootDir, "cosign", ver)
	return err == nil
}

// run resolves cosign and execs it with the rendered args and a class-appropriate
// environment. The sole exec point; warnings carry cosign's stderr for diagnosis.
func run(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, plan sign.SignPlan, op string, args []string) error {
	ver, pinned := toolchain.ResolveVersion(rootDir, "cosign", "", desired)
	result, err := provision.Resolve(ctx, rootDir, "cosign", ver, "artifact signing")
	if err != nil {
		if pinned {
			return fmt.Errorf("cosign %s: pinned version %s failed to resolve: %w", op, ver, err)
		}
		return fmt.Errorf("cosign %s: toolchain resolve: %w", op, err)
	}
	cmd := exec.CommandContext(ctx, result.Path, args...)
	cmd.Env = signEnv(plan)
	out, err := cmd.CombinedOutput()
	if err != nil {
		diag.Warn("cosign %s failed: %s", op, strings.TrimSpace(string(out)))
		return fmt.Errorf("cosign %s: %w", op, err)
	}
	return nil
}

// signEnv builds the process environment for a cosign invocation, per trust class:
//   - key:      hermetic (CleanEnv) — nothing ambient leaks into a key signature.
//   - oidc:     hermetic + the ambient identity (sigstore/CI OIDC token vars).
//   - kms:      hermetic + cloud-provider credential vars.
//   - hardware: the full host environment — the device/PIN agent needs it, and the
//     op is interactive anyway (never unattended-CI-runnable).
//
// All non-key classes still set COSIGN_YES=1 to skip the confirmation prompt.
func signEnv(plan sign.SignPlan) []string {
	if plan.TrustClass == sign.ClassHardware {
		return append(os.Environ(), "COSIGN_YES=1")
	}
	env := append(toolchain.CleanEnv(), "COSIGN_YES=1")
	// Image operations (sign-image, attest) push to a registry and need its
	// credentials; the hermetic CleanEnv (HOME=/tmp) would otherwise hide them.
	// cosign/ggcr read DOCKER_CONFIG, else $HOME/.docker. Honor an explicit
	// DOCKER_CONFIG; otherwise point it at the caller's real ~/.docker so a prior
	// `docker login` is found. Harmless for blob signing (which ignores it).
	// Registry auth is transport, NOT signing input — this never weakens the
	// hermeticity of the signing material itself.
	env = append(env, dockerConfigEnv()...)
	switch plan.TrustClass {
	case sign.ClassKey:
		// A cosign key is always password-encrypted (even with an empty password).
		// Tier-0 auto-provisioned keys use an EMPTY password (the durable state dir
		// is the protection boundary); an operator-set COSIGN_PASSWORD overrides.
		// Emit exactly ONE entry so the result never depends on duplicate-key
		// resolution order in the child process.
		if v, ok := os.LookupEnv("COSIGN_PASSWORD"); ok {
			env = append(env, "COSIGN_PASSWORD="+v)
		} else {
			env = append(env, "COSIGN_PASSWORD=")
		}
	case sign.ClassOIDC:
		env = append(env, forwardByPrefix(oidcEnvPrefixes)...)
	case sign.ClassKMS:
		env = append(env, forwardByPrefix(kmsEnvPrefixes)...)
	}
	return env
}

// oidcEnvPrefixes: sigstore keyless config + the CI workload-identity token vars
// (GitHub Actions, GitLab CI) cosign uses to fetch an ambient OIDC token.
var oidcEnvPrefixes = []string{
	"COSIGN_", "SIGSTORE_", "FULCIO_", "REKOR_", "TUF_",
	"ACTIONS_ID_TOKEN_REQUEST_", "CI_JOB_JWT", "SIGSTORE_ID_TOKEN",
}

// kmsEnvPrefixes: the major cloud-provider credential namespaces a KMS signer needs.
var kmsEnvPrefixes = []string{
	"COSIGN_", "AWS_", "GOOGLE_", "GCP_", "CLOUDSDK_", "AZURE_", "VAULT_",
}

// dockerConfigEnv returns a DOCKER_CONFIG entry pointing at the registry
// credentials a cosign image operation needs, or nil when none can be located.
// An explicit DOCKER_CONFIG wins; otherwise the caller's real ~/.docker is used
// iff it actually holds a config.json (a prior `docker login`).
func dockerConfigEnv() []string {
	if dc, ok := os.LookupEnv("DOCKER_CONFIG"); ok && dc != "" {
		return []string{"DOCKER_CONFIG=" + dc}
	}
	if home := os.Getenv("HOME"); home != "" {
		cfgDir := filepath.Join(home, ".docker")
		if _, err := os.Stat(filepath.Join(cfgDir, "config.json")); err == nil {
			return []string{"DOCKER_CONFIG=" + cfgDir}
		}
	}
	return nil
}

func forwardByPrefix(prefixes []string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name := kv[:strings.IndexByte(kv, '=')]
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}
