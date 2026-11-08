package cosign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
)

// verifyMade proves a signature or attestation the executor just made: cosign verifies it
// against the signer's own public key (a keyless one against its declared identity), with the
// transparency log required whenever the plan requires one. what names it in the error;
// target is the verify arguments after the trust flags (the digest ref, or bundle and blob).
func verifyMade(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, plan sign.SignPlan, env Env, verb, what string, target []string, registryAuthDir string) error {
	args, cleanup, err := verifyArgs(ctx, rootDir, desired, plan, env, verb)
	if err != nil {
		return fmt.Errorf("%s made, but could not be verified: %w", what, err)
	}
	defer cleanup()
	if err := run(ctx, rootDir, desired, plan, verb, append(args, target...), registryAuthDir); err != nil {
		return fmt.Errorf("%s made, but it does not verify: %w", what, err)
	}
	return nil
}

// verifyArgs is the verify invocation for a plan: the signer's public key, exported from the
// same signer the plan signs with, or a keyless signature's identity; the deployment's
// trusted root when one is declared; and the transparency log only when the plan requires it.
func verifyArgs(ctx context.Context, rootDir string, desired map[string]config.ToolConstraint, plan sign.SignPlan, env Env, verb string) ([]string, func(), error) {
	noop := func() {}
	m, err := selectMechanism(plan, env)
	if err != nil {
		return nil, noop, err
	}
	args := []string{verb}
	cleanup := noop
	if m.class == sign.ClassOIDC {
		args = append(args, keylessIdentityArgs(plan, m.sigstore)...)
	} else {
		dir, err := os.MkdirTemp("", "sf-cosign-pub-")
		if err != nil {
			return nil, noop, err
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		pub := filepath.Join(dir, "cosign.pub")
		export := []string{"public-key", "--outfile", pub}
		if m.useSK {
			export = append(export, "--sk")
		} else {
			export = append(export, "--key", m.keyArg)
		}
		if err := run(ctx, rootDir, desired, plan, "public-key", export, ""); err != nil {
			cleanup()
			return nil, noop, err
		}
		args = append(args, "--key", pub)
	}
	if env.Sigstore.TrustedRoot != "" && (m.class == sign.ClassOIDC || plan.TransparencyRequired) {
		args = append(args, "--trusted-root", env.Sigstore.TrustedRoot)
	}
	if !plan.TransparencyRequired {
		args = append(args, "--insecure-ignore-tlog=true")
	}
	return args, cleanup, nil
}

// keylessIdentityArgs is the identity a keyless signature must carry: the plan's declared
// subject and issuer (the deployment's issuer when the plan names none). An identity the
// plan leaves open is matched as any, so the certificate chain and log are still proven.
func keylessIdentityArgs(plan sign.SignPlan, d SigstoreDeployment) []string {
	var a []string
	if plan.Identity.Subject != "" {
		a = append(a, "--certificate-identity", plan.Identity.Subject)
	} else {
		a = append(a, "--certificate-identity-regexp", ".*")
	}
	issuer := plan.Identity.Issuer
	if issuer == "" {
		issuer = d.OIDCIssuer
	}
	if issuer != "" {
		a = append(a, "--certificate-oidc-issuer", issuer)
	} else {
		a = append(a, "--certificate-oidc-issuer-regexp", ".*")
	}
	return a
}
