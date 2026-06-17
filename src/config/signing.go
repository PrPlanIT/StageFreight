package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Signing trust model — the framework-generic primitives. A SigningProfile
// declares a trust CLASS and assurance REQUIREMENTS only; it never names a
// device, vendor, transport, or cosign flag. "Official releases require hardware"
// is PROJECT policy (a target's signing_profile selection), never encoded here —
// the framework enables and verifies many trust models; it does not impose one.
//
// Targets reference a profile by id (signing_profile: <id>) — the same
// reference-by-id pattern as registry: <id>. See
// docs/architecture/signing-trust-model.md.

// SigningProfile is a named, generic trust profile.
type SigningProfile struct {
	ID       string       `yaml:"id"`
	Requires StringOrList `yaml:"requires"` // trust class(es); v1 enforces exactly one

	// Class reference blocks — at most one, matching the declared class.
	Key  *KeyTrust  `yaml:"key,omitempty"`
	OIDC *OIDCTrust `yaml:"oidc,omitempty"`
	KMS  *KMSTrust  `yaml:"kms,omitempty"`

	// Assurance properties (hardware-class ONLY; enforced in validation). The
	// value is the keyword "required" (absent = not required). The renderer later
	// selects a transport that satisfies them — they are never device names here.
	PhysicalPresence string `yaml:"physical_presence,omitempty"`
	NonExportable    string `yaml:"non_exportable,omitempty"`

	// TransparencyLog overrides the per-class default (on for oidc, off otherwise).
	TransparencyLog *bool `yaml:"transparency_log,omitempty"`

	// Attestation also emits a provenance attestation alongside the signature.
	Attestation bool `yaml:"attestation,omitempty"`
}

// KeyTrust is a key reference — "path" or "env:VAR". A reference, not a mechanism.
type KeyTrust struct {
	Ref string `yaml:"ref"`
}

// OIDCTrust is the expected keyless signer identity (both optional).
type OIDCTrust struct {
	Issuer   string `yaml:"issuer,omitempty"`
	Identity string `yaml:"identity,omitempty"`
}

// KMSTrust is a LOGICAL key ref (e.g. "release-signing-key"), bound to a concrete
// URI only at render time — never a cosign URI, so policy stays provider-portable.
type KMSTrust struct {
	Ref string `yaml:"ref"`
}

// Trust classes — the only valid `requires` values after normalization.
const (
	TrustKey      = "key"
	TrustOIDC     = "oidc"
	TrustKMS      = "kms"
	TrustHardware = "hardware"
)

var validTrustClasses = map[string]bool{
	TrustKey: true, TrustOIDC: true, TrustKMS: true, TrustHardware: true,
}

// trustClassAliases normalize convenience names to a canonical class. Machinery
// names that are NOT aliases (fido2, vault, aws, pkcs11) remain invalid classes
// and are rejected by validation — they are transports/providers, not trust.
var trustClassAliases = map[string]string{
	"keyless": TrustOIDC,
	"yubikey": TrustHardware,
}

// legacySigningProfileID is the reserved id of the synthesized default profile.
const legacySigningProfileID = "legacy"

const assuranceRequired = "required"

// StringOrList unmarshals a YAML scalar OR a sequence of strings into []string.
type StringOrList []string

// UnmarshalYAML accepts a bare scalar (coerced to a one-element list) or a
// sequence. Anything else is an error.
func (s *StringOrList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		*s = StringOrList{single}
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*s = list
	default:
		return fmt.Errorf("expected a string or a list of strings")
	}
	return nil
}

// FindSigningProfileByID returns the profile with the given id, or nil.
func FindSigningProfileByID(profiles []SigningProfile, id string) *SigningProfile {
	for i := range profiles {
		if profiles[i].ID == id {
			return &profiles[i]
		}
	}
	return nil
}

// normalizeTrustClass lowercases and applies aliases. The result may still be an
// invalid class — validation rejects unknowns; normalization never errors.
func normalizeTrustClass(raw string) string {
	c := strings.ToLower(strings.TrimSpace(raw))
	if alias, ok := trustClassAliases[c]; ok {
		return alias
	}
	return c
}

// NormalizeSigning canonicalizes `requires` values (alias resolution) and
// synthesizes the implicit `legacy` profile, so a target with no signing_profile
// has a single, uniform compile path (never a parallel legacy branch). Idempotent.
func NormalizeSigning(profiles []SigningProfile) []SigningProfile {
	for i := range profiles {
		for j := range profiles[i].Requires {
			profiles[i].Requires[j] = normalizeTrustClass(profiles[i].Requires[j])
		}
	}
	if FindSigningProfileByID(profiles, legacySigningProfileID) == nil {
		profiles = append(profiles, legacyProfile())
	}
	return profiles
}

// legacyProfile is the synthesized implicit default — key-class signing from
// COSIGN_KEY. The single source of the legacy shape, shared by NormalizeSigning
// (canonical synthesis) and ResolveSigningProfileForTarget (robust fallback when
// a config was built without Normalize, e.g. in tests).
func legacyProfile() SigningProfile {
	return SigningProfile{
		ID:       legacySigningProfileID,
		Requires: StringOrList{TrustKey},
		Key:      &KeyTrust{Ref: "env:COSIGN_KEY"},
	}
}

// ResolvedSigningProfile is the flattened, validated view consumed by the pure
// compiler (src/sign). Exactly one trust class; assurance keywords collapsed to
// booleans; logical refs carried verbatim (bound to keys/URIs only at render).
type ResolvedSigningProfile struct {
	ID               string
	Class            string
	KeyRef           string
	KMSRef           string
	OIDCIssuer       string
	OIDCIdentity     string
	PhysicalPresence bool
	NonExportable    bool
	TransparencyLog  *bool // nil = use the class default
	Attestation      bool
}

// ResolveSigningProfileForTarget returns the resolved profile a target signs
// under. No signing_profile → the synthesized `legacy` profile (single path,
// never nil for a signable target). A set-but-unknown reference is an error.
// Mirrors ResolveRegistryForTarget.
func ResolveSigningProfileForTarget(t TargetConfig, profiles []SigningProfile) (*ResolvedSigningProfile, error) {
	id := t.SigningProfile
	if id == "" {
		id = legacySigningProfileID
	}
	p := FindSigningProfileByID(profiles, id)
	if p == nil {
		// The legacy default always resolves, even on a config that never went
		// through NormalizeSigning. An explicit (non-legacy) unknown ref is still
		// an error — that misconfiguration must surface, not be papered over.
		if id == legacySigningProfileID {
			lp := legacyProfile()
			return resolveSigningProfile(&lp), nil
		}
		return nil, fmt.Errorf("target %s: signing_profile %q not found", t.ID, id)
	}
	return resolveSigningProfile(p), nil
}

func resolveSigningProfile(p *SigningProfile) *ResolvedSigningProfile {
	r := &ResolvedSigningProfile{
		ID:               p.ID,
		TransparencyLog:  p.TransparencyLog,
		Attestation:      p.Attestation,
		PhysicalPresence: strings.EqualFold(p.PhysicalPresence, assuranceRequired),
		NonExportable:    strings.EqualFold(p.NonExportable, assuranceRequired),
	}
	if len(p.Requires) > 0 {
		r.Class = p.Requires[0]
	}
	if p.Key != nil {
		r.KeyRef = p.Key.Ref
	}
	if p.KMS != nil {
		r.KMSRef = p.KMS.Ref
	}
	if p.OIDC != nil {
		r.OIDCIssuer = p.OIDC.Issuer
		r.OIDCIdentity = p.OIDC.Identity
	}
	return r
}

// ValidateTargetSigningProfileRefs checks each target's signing_profile resolves.
// An empty reference is valid (→ the synthesized `legacy` profile); the reserved
// `legacy` id is always valid. Mirrors ValidateTargetRegistryRefs.
func ValidateTargetSigningProfileRefs(targets []TargetConfig, profiles []SigningProfile) []string {
	var errs []string
	for _, t := range targets {
		if t.SigningProfile == "" || t.SigningProfile == legacySigningProfileID {
			continue
		}
		if FindSigningProfileByID(profiles, t.SigningProfile) == nil {
			errs = append(errs, fmt.Sprintf("targets[%s]: signing_profile %q not found", t.ID, t.SigningProfile))
		}
	}
	return errs
}

// ValidateSigningProfiles is the single validation layer for signing policy
// (Compile in src/sign never re-validates — it is total over validated config).
// Alias-tolerant (keyless/yubikey accepted; Normalize canonicalizes later).
func ValidateSigningProfiles(profiles []SigningProfile) []string {
	var errs []string
	seen := map[string]bool{}
	for _, p := range profiles {
		if strings.TrimSpace(p.ID) == "" {
			errs = append(errs, "signing_profiles: an entry has an empty id")
			continue
		}
		if seen[p.ID] {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: duplicate id", p.ID))
		}
		seen[p.ID] = true

		switch {
		case len(p.Requires) == 0:
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: requires is empty (expected one of key, oidc, kms, hardware)", p.ID))
			continue
		case len(p.Requires) > 1:
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: multi-trust composition not yet supported (requires must name exactly one class)", p.ID))
			continue
		}
		// Alias-tolerant: validation accepts keyless/yubikey, which Normalize
		// canonicalizes later. Validate the canonical class without mutating input.
		class := normalizeTrustClass(p.Requires[0])
		if !validTrustClasses[class] {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: unknown trust class %q (expected key, oidc, kms, hardware — not a device/provider)", p.ID, p.Requires[0]))
			continue
		}

		// Class/field coherence — a nested block for a class other than the declared one.
		if p.Key != nil && class != TrustKey {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: key block is invalid for class %q", p.ID, class))
		}
		if p.OIDC != nil && class != TrustOIDC {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: oidc block is invalid for class %q", p.ID, class))
		}
		if p.KMS != nil && class != TrustKMS {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: kms block is invalid for class %q", p.ID, class))
		}

		// Assurance properties are hardware-class-only (invariant 2).
		if (p.PhysicalPresence != "" || p.NonExportable != "") && class != TrustHardware {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: physical_presence/non_exportable are valid only for class hardware", p.ID))
		}
		for name, val := range map[string]string{"physical_presence": p.PhysicalPresence, "non_exportable": p.NonExportable} {
			if val != "" && !strings.EqualFold(val, assuranceRequired) {
				errs = append(errs, fmt.Sprintf("signing_profiles[%s]: %s must be %q (got %q)", p.ID, name, assuranceRequired, val))
			}
		}

		// Required references per class.
		if class == TrustKey && (p.Key == nil || strings.TrimSpace(p.Key.Ref) == "") {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: key.ref is required for class key", p.ID))
		}
		if class == TrustKMS && (p.KMS == nil || strings.TrimSpace(p.KMS.Ref) == "") {
			errs = append(errs, fmt.Sprintf("signing_profiles[%s]: kms.ref is required for class kms", p.ID))
		}
	}
	return errs
}
