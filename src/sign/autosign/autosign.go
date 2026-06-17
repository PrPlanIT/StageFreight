// Package autosign resolves the effective signer for a target, applying Tier-0
// auto-provision ONLY when the operator has consented (enabled && auto_provision &&
// a configured state_dir). It is the one place the "never silently mint a trust
// identity" policy is enforced: with auto_provision off (the default) or no
// state_dir, an unresolved key yields "do not sign", never a freshly-minted key.
package autosign

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
	"github.com/PrPlanIT/StageFreight/src/sign/provision"
)

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
		// An explicit key-class profile whose key does not resolve: fill from Tier-0
		// if consented, preserving the profile's other requirements.
		if plan.TrustClass == sign.ClassKey {
			return tier0Fill(ctx, cfg, plan, rootDir, repoRoot, desired, now)
		}
		return sign.SignPlan{}, "", false, nil // a non-key profile that isn't enabled
	}
	// No explicit profile → sign only if Tier-0 is consented.
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
