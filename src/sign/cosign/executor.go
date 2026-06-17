package cosign

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
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
func SignImage(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig, digestRef string, plan sign.SignPlan, env Env, opts sign.SignOptions) error {
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
func Attest(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig, digestRef string, plan sign.SignPlan, env Env, opts sign.SignOptions) error {
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

// SignBlob signs a detached blob (e.g. SHA256SUMS) per the plan, writing a
// detached signature at <blobPath>.sig and returning its path.
func SignBlob(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig, blobPath string, plan sign.SignPlan, env Env) (string, error) {
	args, err := Render(plan, sign.OpSignBlob, env)
	if err != nil {
		return "", fmt.Errorf("cosign sign-blob: %w", err)
	}
	sigPath := blobPath + ".sig"
	args = append(args, "--output-signature", sigPath, blobPath)
	if err := run(ctx, rootDir, desired, plan, "sign-blob", args); err != nil {
		return "", err
	}
	return sigPath, nil
}

// Available reports whether cosign can be resolved via the toolchain.
func Available(rootDir string, desired map[string]config.ToolPinConfig) bool {
	ver, _ := toolchain.ResolveVersion("cosign", "", desired)
	_, err := toolchain.Resolve(rootDir, "cosign", ver)
	return err == nil
}

// run resolves cosign and execs it with the rendered args and a class-appropriate
// environment. The sole exec point; warnings carry cosign's stderr for diagnosis.
func run(ctx context.Context, rootDir string, desired map[string]config.ToolPinConfig, plan sign.SignPlan, op string, args []string) error {
	ver, pinned := toolchain.ResolveVersion("cosign", "", desired)
	result, err := toolchain.Resolve(rootDir, "cosign", ver)
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
	switch plan.TrustClass {
	case sign.ClassKey:
		// A cosign key is always password-encrypted (even with an empty password),
		// so COSIGN_PASSWORD must reach the subprocess for the key to be usable.
		env = append(env, forwardByPrefix(keyEnvPrefixes)...)
	case sign.ClassOIDC:
		env = append(env, forwardByPrefix(oidcEnvPrefixes)...)
	case sign.ClassKMS:
		env = append(env, forwardByPrefix(kmsEnvPrefixes)...)
	}
	return env
}

// keyEnvPrefixes: the password (and any COSIGN_ tuning) needed to read an
// encrypted key file. The key PATH itself is passed via --key, not the env.
var keyEnvPrefixes = []string{"COSIGN_PASSWORD"}

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
