// Package cosign is the ONLY cosign-aware code in StageFreight signing — the edge
// renderer that satisfies a sign.SignPlan (a declarative trust contract) against a
// declared capability Env, emitting a concrete cosign invocation. It imports sign
// (the pure model); sign never imports it. No interface, no registry: a future
// native signer is just another renderer of SignPlan, added when it exists.
// See docs/architecture/signing-trust-model.md (1e).
package cosign

import (
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

// Principal is a stable trust-principal identity — the model's single load-bearing
// assumption, made explicit AS DATA. For cryptographic classes it is the key's
// public-key fingerprint (derived from key material when the Env is constructed —
// not a free-text label); for oidc it is the (issuer, subject) claim. Render groups
// witnesses by Principal, so identity equivalence is represented in data, never
// inferred from transport/endpoint shape.
type Principal string

// Witnesses — each is one DECLARED reach to a signing capability, carrying the
// Principal it satisfies. The Env is declared (explicitly enumerated by the
// deployment), never auto-probed: declaring rather than discovering prevents a
// plugged-in key from silently changing signing behavior.

// KeyFile is a `key`-class witness (an on-disk / env-referenced key).
type KeyFile struct {
	Principal Principal
	Path      string
}

// KMSKey is a `kms`-class witness (a managed/remote key, addressed by URI).
type KMSKey struct {
	Principal Principal
	URI       string // resolved KMS URI — opaque to core (provider scheme lives only here)
}

// FIDO2Device is a `hardware`-class witness signed via cosign --sk.
type FIDO2Device struct {
	Principal        Principal
	PhysicalPresence bool // touch required to sign
	NonExportable    bool // key generated on-device, never extractable
}

// PKCS11Slot is a `hardware`-class witness signed via cosign --key <pkcs11:...>.
type PKCS11Slot struct {
	Principal        Principal
	URI              string
	PhysicalPresence bool
	NonExportable    bool
}

// OIDCIdentity is an `oidc`-class (keyless) signer claim.
type OIDCIdentity struct {
	Issuer  string
	Subject string
}

// SigstoreDeployment is the named trust DOMAIN an `oidc` (keyless) signature is
// produced against — the answer to "which signing-authority ecosystem vouches for
// this evidence?", not a bag of optional endpoint overrides. A future verification
// engine reasons over this (public vs internal Sigstore, trust-root migration,
// multi-domain policy, mirrored Rekor), so it is modeled as a first-class domain
// even though v1 only carries URLs + a label.
//
// Empty Fulcio/Rekor/TrustedRoot = the public Sigstore default (TUF service
// discovery). Any of them set = a declared (typically self-hosted) deployment the
// renderer points cosign at explicitly. Resolved from SF_SIGSTORE_* by
// EnvForPlan — the renderer reads this witness but never the environment.
type SigstoreDeployment struct {
	Domain        string // human label for the trust domain (e.g. "internal", "prplanit")
	FulcioURL     string // self-hosted Fulcio (CA) — empty = public
	RekorURL      string // self-hosted Rekor (transparency log) — empty = public
	OIDCIssuer    string // the OIDC issuer minting identity tokens (valid for public + self-hosted)
	TrustedRoot   string // path to the trusted-root bundle anchoring this domain's CA + log keys
	IdentityToken string // OIDC token value or path (cosign --identity-token); ambient providers used when empty
}

// Declared reports whether a self-hosted Sigstore deployment is configured — keyed
// on the presence of a service endpoint or trust anchor (NOT Domain, which is only a
// disclosure label, nor OIDCIssuer, which a public deployment may also override).
func (d SigstoreDeployment) Declared() bool {
	return d.FulcioURL != "" || d.RekorURL != "" || d.TrustedRoot != ""
}

// Env is the declared capability graph the deployment exposes to the renderer.
type Env struct {
	Keys     []KeyFile
	KMS      []KMSKey
	FIDO2    []FIDO2Device
	PKCS11   []PKCS11Slot
	OIDC     []OIDCIdentity
	Sigstore SigstoreDeployment // oidc/keyless trust domain (single deployment, not a principal set)
}

// SigstoreDomain returns the trust-domain label to RECORD for an oidc/keyless
// signature — the named ecosystem that vouched for it. Empty for non-oidc classes
// (no Sigstore domain). For oidc: the explicit Domain label if set, else the Fulcio
// host for a declared self-hosted deployment, else "public-sigstore". Mirrors the
// Render decision so the recorded evidence matches where the signature actually went.
func SigstoreDomain(plan sign.SignPlan, env Env) string {
	if plan.TrustClass != sign.ClassOIDC {
		return ""
	}
	if env.Sigstore.Domain != "" {
		return env.Sigstore.Domain
	}
	if env.Sigstore.Declared() {
		if h := hostOf(env.Sigstore.FulcioURL); h != "" {
			return h
		}
		return "self-hosted-sigstore"
	}
	return "public-sigstore"
}

// hostOf extracts a bare host label from a URL for disclosure (no scheme/port/path).
func hostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return s
}

// EnvForPlan resolves a plan's logical references against the ambient environment
// into a declared witness Env — the impure resolution boundary that keeps Render
// itself pure over (plan, op, env). It resolves what has a REF (key/kms/oidc, and the
// hardware PKCS#11 transport); ref-less hardware witnesses (a present FIDO2 token) are
// declared externally by the caller and merged in.
func EnvForPlan(plan sign.SignPlan) Env {
	var env Env
	switch plan.TrustClass {
	case sign.ClassKey:
		if path := sign.DerefKeyRef(plan.KeyRef); path != "" {
			env.Keys = []KeyFile{{Path: path}}
		}
	case sign.ClassKMS:
		if uri := resolveKMSURI(plan.KMSRef); uri != "" {
			env.KMS = []KMSKey{{URI: uri}}
		}
	case sign.ClassOIDC:
		env.OIDC = []OIDCIdentity{{Issuer: plan.Identity.Issuer, Subject: plan.Identity.Subject}}
		env.Sigstore = resolveSigstoreDeployment(plan)
	case sign.ClassHardware:
		// PKCS#11 is the RESOLVABLE hardware transport — a logical ref bound to a
		// pkcs11: URI via SF_PKCS11_<REF> (parallel to kms). FIDO2 witnesses carry no
		// ref and are declared externally (sign.go's envForClass). A hardware-token key
		// is non-exportable by nature and touch-capable, so the witness satisfies any
		// hardware assurance; the RECORDED evidence still reflects the profile's
		// declared policy, not the witness.
		if plan.PKCS11Ref != "" {
			if uri := resolvePKCS11URI(plan.PKCS11Ref); uri != "" {
				env.PKCS11 = []PKCS11Slot{{
					Principal:        Principal(plan.PKCS11Ref),
					URI:              uri,
					PhysicalPresence: true,
					NonExportable:    true,
				}}
			}
		}
	}
	return env
}

// resolveSigstoreDeployment binds the oidc trust domain from SF_SIGSTORE_*
// (deployment wiring, parallel to SF_KMS_* for kms). Pure env substitution —
// the renderer never reads these. The issuer falls back to the profile's declared
// identity issuer when the env var is unset, so a profile stays portable while the
// operator says "…via my Fulcio."
func resolveSigstoreDeployment(plan sign.SignPlan) SigstoreDeployment {
	d := SigstoreDeployment{
		Domain:        os.Getenv("SF_SIGSTORE_DOMAIN"),
		FulcioURL:     os.Getenv("SF_SIGSTORE_FULCIO"),
		RekorURL:      os.Getenv("SF_SIGSTORE_REKOR"),
		OIDCIssuer:    os.Getenv("SF_SIGSTORE_ISSUER"),
		TrustedRoot:   os.Getenv("SF_SIGSTORE_TRUSTED_ROOT"),
		IdentityToken: os.Getenv("SF_SIGSTORE_IDENTITY_TOKEN"),
	}
	if d.OIDCIssuer == "" {
		d.OIDCIssuer = plan.Identity.Issuer
	}
	return d
}
