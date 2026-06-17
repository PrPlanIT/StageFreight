// Package cosign is the ONLY cosign-aware code in StageFreight signing — the edge
// renderer that satisfies a sign.SignPlan (a declarative trust contract) against a
// declared capability Env, emitting a concrete cosign invocation. It imports sign
// (the pure model); sign never imports it. No interface, no registry: a future
// native signer is just another renderer of SignPlan, added when it exists.
// See docs/architecture/signing-trust-model.md (1e).
package cosign

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

// Env is the declared capability graph the deployment exposes to the renderer.
type Env struct {
	Keys   []KeyFile
	KMS    []KMSKey
	FIDO2  []FIDO2Device
	PKCS11 []PKCS11Slot
	OIDC   []OIDCIdentity
}
