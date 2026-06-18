// Package sign is the pure trust-contract core of StageFreight signing. It owns
// the neutral IR (SignPlan) and the total Compile lowering — the stable model
// that insulates the rest of the system from cosign. It imports config (which
// owns the policy structs + validation) and NEVER imports cosign: the cosign
// renderer (src/sign/cosign) is an edge that satisfies these requirements, not a
// participant in the model. See docs/architecture/signing-trust-model.md.
package sign

import (
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// Class is a trust class — the kind of authority a signature carries.
type Class string

const (
	ClassKey      Class = "key"
	ClassOIDC     Class = "oidc"
	ClassKMS      Class = "kms"
	ClassHardware Class = "hardware"
)

// Op is a signing operation; the renderer maps each to a concrete invocation.
type Op string

const (
	OpSignImage Op = "sign-image"
	OpAttest    Op = "attest"
	OpSignBlob  Op = "sign-blob"
)

// IdentityConstraints expresses an expected signer identity (oidc/keyless). Both
// fields optional; empty = no constraint (record-only at v1).
type IdentityConstraints struct {
	Issuer  string
	Subject string
}

// SignPlan is the insulation boundary AS DATA — a declarative trust CONTRACT
// ("what must be true for a signature to be acceptable"), never an execution
// plan. It deliberately carries NO cosign vocabulary (no --sk/--key/mode/URI):
// the only code that turns these requirements into an invocation is the renderer,
// by satisfying them against a declared capability Env. Renderer-shaped fields
// (UseKeyless, CosignMode, UploadTlog, …) must never appear here — that erosion
// is exactly what collapses the "cosign as edge renderer" invariant.
type SignPlan struct {
	// ── Trust requirements (what must be true) ──
	TrustClass               Class
	TransparencyRequired     bool // the signature must be recorded in a transparency log
	RequiresPhysicalPresence bool // the signer must demonstrate physical presence
	RequiresNonExportableKey bool // the signing key must be hardware-bound / non-exportable
	Identity                 IdentityConstraints

	// ── Logical references (bound to keys/URIs only at render time) ──
	KeyRef string // key class: "path" or "env:VAR"
	KMSRef string // kms class: a logical ref, bound to a URI by the renderer

	// ── Execution modifier (kept distinct so policy logic never leaks here) ──
	Attestation bool // also emit a provenance attestation
}

// SignOptions carry per-invocation inputs that are not trust requirements.
type SignOptions struct {
	MultiArch     bool
	PredicatePath string
	PredicateType string // attestation predicate type (e.g. "slsaprovenance"); caller-set, never executor-defaulted
}

// SignerRef returns the signer identity material a plan signs under, for
// recording as trust evidence (never for execution). Key/kms: the logical ref;
// oidc: the (issuer, subject) identity; empty when nothing identifies the signer.
func SignerRef(p SignPlan) string {
	switch p.TrustClass {
	case ClassKey:
		return p.KeyRef
	case ClassKMS:
		return p.KMSRef
	case ClassOIDC:
		if p.Identity.Issuer != "" || p.Identity.Subject != "" {
			return p.Identity.Issuer + "/" + p.Identity.Subject
		}
	}
	return ""
}

// Compile lowers a validated, resolved profile to its trust contract. It is a
// deterministic, TOTAL transform: config.ValidateSigningProfiles is the single
// validation layer, so Compile has no error path and never re-validates. Purity
// is over the LOGICAL policy — refs stay logical (KeyRef/KMSRef are resolved to
// concrete keys/URIs only at render time, never here).
func Compile(p *config.ResolvedSigningProfile) SignPlan {
	plan := SignPlan{
		TrustClass:  Class(p.Class),
		Attestation: p.Attestation,
		Identity:    IdentityConstraints{Issuer: p.OIDCIssuer, Subject: p.OIDCIdentity},
		KeyRef:      p.KeyRef,
		KMSRef:      p.KMSRef,
	}
	if plan.TrustClass == ClassHardware {
		plan.RequiresPhysicalPresence = p.PhysicalPresence
		plan.RequiresNonExportableKey = p.NonExportable
	}
	// Transparency requirement: per-class default (on for oidc, off otherwise),
	// overridable by the profile's explicit transparency_log.
	plan.TransparencyRequired = plan.TrustClass == ClassOIDC
	if p.TransparencyLog != nil {
		plan.TransparencyRequired = *p.TransparencyLog
	}
	return plan
}

// Enabled reports whether a plan should actually sign. It preserves today's
// no-key-no-signing for the implicit `legacy` key path: a key-class plan whose
// key reference does not resolve is disabled (no signature, not an error). This
// is the only env-dependent function here; Compile stays pure.
func Enabled(p SignPlan) bool {
	if p.TrustClass == ClassKey {
		return resolveKeyRef(p.KeyRef) != ""
	}
	// oidc/kms/hardware plans exist only when a profile explicitly configured them.
	return p.TrustClass != ""
}

// DerefKeyRef resolves a key reference to its concrete value WITHOUT existence
// checking: "env:VAR" → the environment value; otherwise the path verbatim. The one
// canonical place the "env:VAR vs path" grammar is parsed — shared by Enabled (which
// adds an existence check) and the renderer's witness resolution, so the two can
// never diverge.
func DerefKeyRef(ref string) string {
	if ref == "" {
		return ""
	}
	if v, ok := strings.CutPrefix(ref, "env:"); ok {
		return os.Getenv(v)
	}
	return ref
}

// resolveKeyRef resolves a key reference and confirms it is usable: an env value is
// returned as-is; a filesystem path is returned iff it exists. Empty = unresolved
// (signing disabled for the legacy no-key path).
func resolveKeyRef(ref string) string {
	v := DerefKeyRef(ref)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(ref, "env:") {
		if _, err := os.Stat(v); err != nil {
			return ""
		}
	}
	return v
}
