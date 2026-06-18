package config

import "testing"

func TestNormalizeSigning_LegacyAndAliases(t *testing.T) {
	out := NormalizeSigning([]SigningProfile{
		{ID: "dev", Requires: StringOrList{"keyless"}},
		{ID: "rel", Requires: StringOrList{"yubikey"}},
	})
	if FindSigningProfileByID(out, "dev").Requires[0] != TrustOIDC {
		t.Error("keyless must normalize to oidc")
	}
	if FindSigningProfileByID(out, "rel").Requires[0] != TrustHardware {
		t.Error("yubikey must normalize to hardware")
	}
	legacy := FindSigningProfileByID(out, "legacy")
	if legacy == nil || legacy.Requires[0] != TrustKey || legacy.Key == nil || legacy.Key.Ref != "env:COSIGN_KEY" {
		t.Errorf("legacy profile not synthesized: %+v", legacy)
	}
	if len(NormalizeSigning(out)) != len(out) {
		t.Error("NormalizeSigning must be idempotent (no duplicate legacy)")
	}
}

func TestValidateSigningProfiles_Invariants(t *testing.T) {
	cases := []struct {
		name    string
		p       SigningProfile
		wantErr bool
	}{
		{"valid oidc", SigningProfile{ID: "a", Requires: StringOrList{"oidc"}}, false},
		{"valid hardware w/ assurance", SigningProfile{ID: "a", Requires: StringOrList{"hardware"}, PhysicalPresence: "required", NonExportable: "required"}, false},
		{"keyless alias accepted", SigningProfile{ID: "a", Requires: StringOrList{"keyless"}}, false},
		{"multi-trust deferred", SigningProfile{ID: "a", Requires: StringOrList{"hardware", "oidc"}}, true},
		{"machinery name rejected", SigningProfile{ID: "a", Requires: StringOrList{"fido2"}}, true},
		{"assurance on non-hardware rejected", SigningProfile{ID: "a", Requires: StringOrList{"oidc"}, PhysicalPresence: "required"}, true},
		{"key block on oidc rejected", SigningProfile{ID: "a", Requires: StringOrList{"oidc"}, Key: &KeyTrust{Ref: "x"}}, true},
		{"kms missing ref rejected", SigningProfile{ID: "a", Requires: StringOrList{"kms"}}, true},
		{"key missing ref rejected", SigningProfile{ID: "a", Requires: StringOrList{"key"}}, true},
		{"empty id rejected", SigningProfile{Requires: StringOrList{"oidc"}}, true},
		{"bad assurance value rejected", SigningProfile{ID: "a", Requires: StringOrList{"hardware"}, PhysicalPresence: "maybe"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errs := ValidateSigningProfiles([]SigningProfile{tc.p}); (len(errs) > 0) != tc.wantErr {
				t.Errorf("errs=%v wantErr=%v", errs, tc.wantErr)
			}
		})
	}
}

func TestResolveSigningProfileForTarget(t *testing.T) {
	yes := true
	profiles := NormalizeSigning([]SigningProfile{
		{ID: "rel", Requires: StringOrList{"hardware"}, PhysicalPresence: "required", NonExportable: "required", TransparencyLog: &yes},
	})

	// no signing_profile → synthesized legacy (single path, never nil)
	if r, err := ResolveSigningProfileForTarget(TargetConfig{ID: "t"}, profiles); err != nil || r.ID != "legacy" || r.Class != TrustKey {
		t.Errorf("empty should resolve to legacy/key: %+v %v", r, err)
	}
	// explicit ref → resolves, assurance keywords flatten to booleans
	if r, err := ResolveSigningProfileForTarget(TargetConfig{ID: "t", SigningProfile: "rel"}, profiles); err != nil || r.Class != TrustHardware || !r.PhysicalPresence || !r.NonExportable {
		t.Errorf("rel resolution wrong: %+v %v", r, err)
	}
	// unknown ref → error
	if _, err := ResolveSigningProfileForTarget(TargetConfig{ID: "t", SigningProfile: "nope"}, profiles); err == nil {
		t.Error("unknown signing_profile must error")
	}
}

func TestValidateTargetSigningProfileRefs(t *testing.T) {
	profiles := []SigningProfile{{ID: "rel", Requires: StringOrList{"hardware"}}}
	targets := []TargetConfig{
		{ID: "a", SigningProfile: "rel"},    // ok
		{ID: "b"},                           // empty → legacy, ok
		{ID: "c", SigningProfile: "legacy"}, // reserved, ok
		{ID: "d", SigningProfile: "ghost"},  // unknown → error
	}
	errs := ValidateTargetSigningProfileRefs(targets, profiles)
	if len(errs) != 1 {
		t.Errorf("expected exactly one error (target d), got %v", errs)
	}
}

func TestValidate_NonExportableAllowedOnKMS(t *testing.T) {
	if errs := ValidateSigningProfiles([]SigningProfile{
		{ID: "v", Requires: StringOrList{"kms"}, KMS: &KMSTrust{Ref: "rel"}, NonExportable: "required"},
	}); len(errs) != 0 {
		t.Errorf("non_exportable on a kms profile must validate: %v", errs)
	}
	if errs := ValidateSigningProfiles([]SigningProfile{
		{ID: "v", Requires: StringOrList{"kms"}, KMS: &KMSTrust{Ref: "rel"}, PhysicalPresence: "required"},
	}); len(errs) == 0 {
		t.Error("physical_presence on a kms profile must be rejected (hardware-only)")
	}
}
