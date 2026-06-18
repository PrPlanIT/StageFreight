package autosign

import (
	"context"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func bp(b bool) *bool { return &b }

// The kill switch disables signing even with a fully-consented Tier-0 config.
func TestEffectiveSigner_KillSwitch(t *testing.T) {
	cfg := config.SigningConfig{Enabled: bp(false), AutoProvision: true, StateDir: config.StateDir{Type: "host_path", Path: "/tmp/x"}}
	if _, _, ok, err := EffectiveSigner(context.Background(), cfg, nil, "/repo", "/repo", nil, "now"); ok || err != nil {
		t.Fatalf("kill switch must disable signing: ok=%v err=%v", ok, err)
	}
}

// No profile + no consent must NOT sign — and must not silently mint a key.
func TestEffectiveSigner_NoProfileNoConsent(t *testing.T) {
	if _, _, ok, err := EffectiveSigner(context.Background(), config.SigningConfig{}, nil, "/repo", "/repo", nil, "now"); ok || err != nil {
		t.Fatalf("no profile + auto_provision off must not sign: ok=%v err=%v", ok, err)
	}
}

// auto_provision on but no state_dir → no signer (and no mint), never a panic.
func TestEffectiveSigner_AutoProvisionWithoutStateDir(t *testing.T) {
	cfg := config.SigningConfig{AutoProvision: true}
	if _, _, ok, err := EffectiveSigner(context.Background(), cfg, nil, "/repo", "/repo", nil, "now"); ok || err != nil {
		t.Fatalf("auto_provision without state_dir must not sign: ok=%v err=%v", ok, err)
	}
}

// An explicit, resolvable key profile signs as the operator-supplied signer (no tier).
func TestEffectiveSigner_ExplicitKeyProfileResolves(t *testing.T) {
	t.Setenv("COSIGN_KEY", "/keys/cosign.key")
	profile := &config.ResolvedSigningProfile{Class: "key", KeyRef: "env:COSIGN_KEY"}
	plan, tier, ok, err := EffectiveSigner(context.Background(), config.SigningConfig{}, profile, "/repo", "/repo", nil, "now")
	if err != nil || !ok {
		t.Fatalf("explicit resolvable key profile must sign: ok=%v err=%v", ok, err)
	}
	if tier != "" {
		t.Errorf("operator-supplied signer carries no auto tier, got %q", tier)
	}
	if string(plan.TrustClass) != "key" {
		t.Errorf("expected key class, got %q", plan.TrustClass)
	}
}

// An EXPLICIT profile whose signer does not resolve must fail loudly — never
// silently downgrade to a weaker auto-provisioned key.
func TestEffectiveSigner_ExplicitUnresolvedIsFatal(t *testing.T) {
	profile := &config.ResolvedSigningProfile{ID: "release-key", Class: "key", KeyRef: "env:SF_UNSET_KEY_XYZ"}
	_, _, ok, err := EffectiveSigner(context.Background(), config.SigningConfig{}, profile, "/repo", "/repo", nil, "now")
	if err == nil || ok {
		t.Fatalf("an unresolved explicit signer must be fatal, got ok=%v err=%v", ok, err)
	}
}

// The legacy implicit default with no key + no consent skips silently (back-compat
// no-key-no-signing), NOT a fatal — it is the always-on path, not an explicit demand.
func TestEffectiveSigner_LegacyUnresolvedSkipsSilently(t *testing.T) {
	profile := &config.ResolvedSigningProfile{ID: "legacy", Class: "key", KeyRef: "env:SF_UNSET_KEY_XYZ"}
	_, _, ok, err := EffectiveSigner(context.Background(), config.SigningConfig{}, profile, "/repo", "/repo", nil, "now")
	if err != nil || ok {
		t.Fatalf("legacy default with no key must skip silently, got ok=%v err=%v", ok, err)
	}
}

func TestInactiveReason(t *testing.T) {
	if r := InactiveReason(config.SigningConfig{Enabled: bp(false)}); !strings.Contains(r, "disabled") {
		t.Errorf("disabled reason: %q", r)
	}
	if r := InactiveReason(config.SigningConfig{}); !strings.Contains(r, "auto-provision is not configured") {
		t.Errorf("no-config reason: %q", r)
	}
	if r := InactiveReason(config.SigningConfig{StateDir: config.StateDir{Type: "volume"}}); !strings.Contains(r, "auto_provision is false") {
		t.Errorf("state-dir-but-no-autoprovision reason: %q", r)
	}
}
