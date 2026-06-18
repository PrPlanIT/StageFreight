// Package autosign resolves the effective signer for a target, applying Tier-0
// auto-provision ONLY when the operator has consented (enabled && auto_provision &&
// a configured state_dir). It is the one place the "never silently mint a trust
// identity" policy is enforced: with auto_provision off (the default) or no
// state_dir, an unresolved key yields "do not sign", never a freshly-minted key.
package autosign

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

// SigningContext is the REALIZED signing semantics for a target: the resolved signer
// plan, the declared capability Env, the assurance tier, and whether to sign. It is a
// SEMANTIC context object, deliberately NOT an orchestration one — it holds no
// artifact refs, no result manifests, no recorders, and no publication handles. The
// call sites keep ownership of transport (image vs blob), phase (Build sign vs Publish
// attest), and recording sink; this owns "what signer, what env, what evidence" so
// every site resolves them IDENTICALLY. Adding a new evidence dimension is then a
// one-place change here, not a synchronized edit across four hand-rolled sites.
type SigningContext struct {
	Plan   sign.SignPlan
	Env    cosign.Env
	Tier   string
	DoSign bool
}

// ResolveSigningContext resolves the effective signer (EffectiveSigner) and binds its
// declared capability Env in one step — the canonical path for the build sites
// (auto-sign with consented Tier-0 fallback). DoSign==false means no signer resolved
// (the caller skips); a non-nil error is FATAL (continuity / state-dir guard /
// explicit-profile no-downgrade), surfaced unchanged. sign.go does NOT use this: a
// human-chosen profile compiles directly with no Tier-0 fallback, so it builds a
// SigningContext itself and shares only Evidence().
func ResolveSigningContext(ctx context.Context, cfg config.SigningConfig, profile *config.ResolvedSigningProfile, rootDir, repoRoot string, desired map[string]config.ToolPinConfig, now string) (SigningContext, error) {
	plan, tier, doSign, err := EffectiveSigner(ctx, cfg, profile, rootDir, repoRoot, desired, now)
	if err != nil {
		return SigningContext{}, err
	}
	if !doSign {
		return SigningContext{}, nil
	}
	return SigningContext{Plan: plan, Env: cosign.EnvForPlan(plan), Tier: tier, DoSign: true}, nil
}

// Evidence is the CANONICAL TrustEvidence for this context — the single definition of
// how a realized signer becomes recorded trust facts. Every signing site routes
// through here so class, tier, presence, non-exportability, transparency, signer ref,
// and trust domain are populated consistently (the omission of any one was the exact
// drift this consolidates). signedAt is an RFC3339 timestamp ("" to omit).
func (c SigningContext) Evidence(signedAt string) artifact.TrustEvidence {
	return artifact.TrustEvidence{
		TrustClass:       string(c.Plan.TrustClass),
		Tier:             c.Tier,
		PhysicalPresence: c.Plan.RequiresPhysicalPresence,
		NonExportable:    c.Plan.RequiresNonExportableKey,
		Transparency:     c.Plan.TransparencyRequired,
		SignerRef:        sign.SignerRef(c.Plan),
		SignedAt:         signedAt,
		TrustDomain:      cosign.SigstoreDomain(c.Plan, c.Env),
	}
}

// EffectiveSigner resolves the SignPlan a target signs under. `profile` may be nil
// (no explicit profile). Returns:
//   - plan: the resolved signing plan
//   - tier: assurance tier ("" for an operator-supplied signer; "tier0-software"
//     when auto-provisioned) — recorded so trust is never overstated
//   - ok:   whether signing should proceed
//   - err:  FATAL (continuity violation, state-dir guard, resolve failure)
func EffectiveSigner(ctx context.Context, cfg config.SigningConfig, profile *config.ResolvedSigningProfile, rootDir, repoRoot string, desired map[string]config.ToolPinConfig, now string) (sign.SignPlan, string, bool, error) {
	if !cfg.SigningEnabled() {
		return sign.SignPlan{}, "", false, nil // global kill switch
	}
	if profile != nil {
		plan := sign.Compile(profile)
		if sign.Enabled(plan) {
			return plan, "", true, nil // operator-supplied signer resolves
		}
		// The synthesized `legacy` default (target has no explicit signing_profile)
		// is the always-on path: fall to Tier-0 if consented, else silently skip
		// (preserving today's no-key-no-signing).
		if profile.IsLegacyDefault() {
			return tier0Fill(ctx, cfg, plan, rootDir, repoRoot, desired, now)
		}
		// An EXPLICIT profile that does not resolve is FATAL — an operator's stated
		// trust expectation must fail loudly when unmet, never silently downgrade to
		// a weaker auto-provisioned key. Opt in with allow_fallback to permit Tier-0.
		if plan.TrustClass == sign.ClassKey && profile.AllowFallback {
			return tier0Fill(ctx, cfg, plan, rootDir, repoRoot, desired, now)
		}
		return sign.SignPlan{}, "", false, fmt.Errorf("signing profile %q is configured but did not resolve to a usable signer — refusing to silently downgrade (set allow_fallback: true to permit Tier-0 fallback)", profile.ID)
	}
	// No explicit profile at all → sign only if Tier-0 is consented.
	return tier0Fill(ctx, cfg, sign.SignPlan{TrustClass: sign.ClassKey}, rootDir, repoRoot, desired, now)
}

func tier0Fill(ctx context.Context, cfg config.SigningConfig, plan sign.SignPlan, rootDir, repoRoot string, desired map[string]config.ToolPinConfig, now string) (sign.SignPlan, string, bool, error) {
	if !cfg.AutoProvision || !cfg.StateDir.Configured() {
		return sign.SignPlan{}, "", false, nil // not consented / nowhere to persist
	}
	stateDir, err := cfg.StateDir.Resolve()
	if err != nil {
		return sign.SignPlan{}, "", false, err
	}
	if err := provision.GuardStateDir(stateDir, repoRoot); err != nil {
		return sign.SignPlan{}, "", false, err
	}
	id, err := provision.EnsureIdentity(ctx, stateDir, cosign.KeyGen{RootDir: rootDir, Desired: desired}, now)
	if err != nil {
		return sign.SignPlan{}, "", false, err // FATAL continuity error
	}
	plan.TrustClass = sign.ClassKey
	plan.KeyRef = id.KeyPath(stateDir)
	return plan, id.Tier, true, nil
}

// InactiveReason explains — for a highly-visible advisory — why signing did not run
// despite being enabled, so an operator never silently produces unsigned artifacts
// believing signing is on.
func InactiveReason(cfg config.SigningConfig) string {
	switch {
	case !cfg.SigningEnabled():
		return "signing is disabled (signing.enabled: false)"
	case !cfg.StateDir.Configured() && !cfg.AutoProvision:
		return "no usable signer resolved — no COSIGN_KEY/profile, and Tier-0 auto-provision is not configured (set signing.auto_provision: true + signing.state_dir, or supply a key/profile)"
	case !cfg.AutoProvision:
		return "no usable signer resolved — no COSIGN_KEY/profile, and signing.auto_provision is false (Tier-0 identity creation requires explicit consent)"
	case !cfg.StateDir.Configured():
		return "no usable signer resolved — signing.auto_provision is true but signing.state_dir is not configured (nowhere to persist a Tier-0 identity)"
	default:
		return "no usable signer resolved"
	}
}
