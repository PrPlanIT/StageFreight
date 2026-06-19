package cosign

import (
	"slices"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

func TestRender_KeyClass(t *testing.T) {
	t.Setenv("SF_TEST_KEY", "/keys/cosign.key")
	p := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:SF_TEST_KEY"}
	args, err := Render(p, sign.OpSignImage, EnvForPlan(p))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{"sign", "--key", "/keys/cosign.key", "--use-signing-config=false", "--tlog-upload=false", "--yes"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestRender_KeyClass_UnresolvedRefErrors(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:SF_MISSING_KEY"}
	if _, err := Render(p, sign.OpSignImage, EnvForPlan(p)); err == nil {
		t.Fatal("expected error for unresolved key ref")
	}
}

func TestRender_OIDCKeyless(t *testing.T) {
	// oidc is keyless: no --key, transparency default on (carried in the plan).
	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true}
	args, err := Render(p, sign.OpSignImage, Env{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if slices.Contains(args, "--key") {
		t.Errorf("keyless render must not emit --key: %v", args)
	}
	if !slices.Contains(args, "--use-signing-config=true") {
		t.Errorf("oidc must use the signing-config (transparency log): %v", args)
	}
	// cosign v3 requires the new bundle format alongside the TUF signing-config.
	if !slices.Contains(args, "--new-bundle-format=true") {
		t.Errorf("public signing-config path must set --new-bundle-format=true: %v", args)
	}
	if slices.Contains(args, "--tlog-upload=false") {
		t.Errorf("transparency-required render must not disable the tlog: %v", args)
	}
}

// A DECLARED (self-hosted) Sigstore deployment must override public TUF discovery:
// explicit Fulcio/Rekor/trusted-root + issuer + identity token, and
// --use-signing-config=false (public Fulcio won't trust a self-hosted issuer).
func TestRender_OIDC_SelfHostedDeployment(t *testing.T) {
	t.Setenv("SF_SIGSTORE_DOMAIN", "internal")
	t.Setenv("SF_SIGSTORE_FULCIO", "https://fulcio.internal.example")
	t.Setenv("SF_SIGSTORE_REKOR", "https://rekor.internal.example")
	t.Setenv("SF_SIGSTORE_ISSUER", "https://id.internal.example")
	t.Setenv("SF_SIGSTORE_TRUSTED_ROOT", "/etc/sf/trusted-root.json")
	t.Setenv("SF_SIGSTORE_IDENTITY_TOKEN", "/run/secrets/oidc-token")

	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true}
	args, err := Render(p, sign.OpSignImage, EnvForPlan(p))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, pair := range [][2]string{
		{"--fulcio-url", "https://fulcio.internal.example"},
		{"--rekor-url", "https://rekor.internal.example"},
		{"--oidc-issuer", "https://id.internal.example"},
		{"--trusted-root", "/etc/sf/trusted-root.json"},
		{"--identity-token", "/run/secrets/oidc-token"},
	} {
		if !flagHasValue(args, pair[0], pair[1]) {
			t.Errorf("expected %s %s in %v", pair[0], pair[1], args)
		}
	}
	if !slices.Contains(args, "--use-signing-config=false") {
		t.Errorf("self-hosted keyless must disable public TUF service discovery: %v", args)
	}
	if slices.Contains(args, "--use-signing-config=true") || slices.Contains(args, "--new-bundle-format=true") {
		t.Errorf("self-hosted path must not take the public signing-config branch: %v", args)
	}
	if slices.Contains(args, "--key") {
		t.Errorf("keyless render must not emit --key: %v", args)
	}
}

// Self-hosted WITHOUT transparency: no --rekor-url, tlog disabled.
func TestRender_OIDC_SelfHostedNoTransparency(t *testing.T) {
	t.Setenv("SF_SIGSTORE_FULCIO", "https://fulcio.internal.example")
	t.Setenv("SF_SIGSTORE_REKOR", "https://rekor.internal.example")
	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: false}
	args, err := Render(p, sign.OpSignBlob, EnvForPlan(p))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if slices.Contains(args, "--rekor-url") {
		t.Errorf("no-transparency render must not point at Rekor: %v", args)
	}
	if !slices.Contains(args, "--tlog-upload=false") {
		t.Errorf("no-transparency render must disable the tlog: %v", args)
	}
}

// The issuer falls back to the profile's declared identity issuer when the env var
// is unset — a profile stays portable while an operator supplies the deployment.
func TestRender_OIDC_IssuerFallsBackToProfileIdentity(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true,
		Identity: sign.IdentityConstraints{Issuer: "https://gitlab.example/oauth"}}
	args, err := Render(p, sign.OpSignImage, EnvForPlan(p))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !flagHasValue(args, "--oidc-issuer", "https://gitlab.example/oauth") {
		t.Errorf("issuer should fall back to the profile identity: %v", args)
	}
}

// flagHasValue reports whether args contains flag immediately followed by value.
func flagHasValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestRender_KMSClass(t *testing.T) {
	t.Setenv("SF_KMS_RELEASE", "awskms://alias/release")
	p := sign.SignPlan{TrustClass: sign.ClassKMS, KMSRef: "release"}
	args, err := Render(p, sign.OpSignImage, EnvForPlan(p))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !slices.Contains(args, "awskms://alias/release") {
		t.Errorf("kms URI not bound into args: %v", args)
	}
}

func TestRender_KMSClass_UnboundRefErrors(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassKMS, KMSRef: "release"}
	if _, err := Render(p, sign.OpSignImage, EnvForPlan(p)); err == nil {
		t.Fatal("expected error for unbound kms ref")
	}
}

// |D| == 0 — nothing in the Env satisfies the contract.
func TestRender_Hardware_NoDevice_Errors(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassHardware, RequiresPhysicalPresence: true}
	if _, err := Render(p, sign.OpSignImage, Env{}); err == nil {
		t.Fatal("expected error when no device satisfies the contract")
	}
}

// |D| == 1 via FIDO2 → --sk.
func TestRender_Hardware_SingleFIDO2(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassHardware, RequiresPhysicalPresence: true, RequiresNonExportableKey: true}
	env := Env{FIDO2: []FIDO2Device{{Principal: "yk-A", PhysicalPresence: true, NonExportable: true}}}
	args, err := Render(p, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !slices.Contains(args, "--sk") {
		t.Errorf("expected --sk for a FIDO2 token: %v", args)
	}
}

// |D| == 1 via PKCS#11 only → --key <uri>.
func TestRender_Hardware_SinglePKCS11(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassHardware, RequiresNonExportableKey: true}
	env := Env{PKCS11: []PKCS11Slot{{Principal: "hsm-A", URI: "pkcs11:slot=0", NonExportable: true}}}
	args, err := Render(p, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !slices.Contains(args, "pkcs11:slot=0") {
		t.Errorf("expected the PKCS#11 URI in args: %v", args)
	}
}

// One principal reachable two ways is |D| == 1 — a transport choice, not a trust
// ambiguity. Deterministic preference: FIDO2 (--sk) over PKCS#11.
func TestRender_Hardware_OnePrincipalTwoTransports(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassHardware, RequiresNonExportableKey: true}
	env := Env{
		FIDO2:  []FIDO2Device{{Principal: "key-A", NonExportable: true}},
		PKCS11: []PKCS11Slot{{Principal: "key-A", URI: "pkcs11:slot=0", NonExportable: true}},
	}
	args, err := Render(p, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("one principal must resolve, not error: %v", err)
	}
	if !slices.Contains(args, "--sk") {
		t.Errorf("expected deterministic FIDO2 preference: %v", args)
	}
}

// |D| > 1 — distinct keys could each sign. A trust ambiguity → refuse.
func TestRender_Hardware_DistinctPrincipals_Errors(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassHardware, RequiresNonExportableKey: true}
	env := Env{FIDO2: []FIDO2Device{
		{Principal: "key-A", NonExportable: true},
		{Principal: "key-B", NonExportable: true},
	}}
	_, err := Render(p, sign.OpSignImage, env)
	if err == nil || !strings.Contains(err.Error(), "ambiguity") {
		t.Fatalf("expected a trust-ambiguity error, got: %v", err)
	}
}

// A device that does not meet the required assurance is not a satisfying witness.
func TestRender_Hardware_AssuranceFilter(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassHardware, RequiresPhysicalPresence: true}
	// Present but no physical-presence → filtered out → |D| == 0 → error.
	env := Env{FIDO2: []FIDO2Device{{Principal: "key-A", PhysicalPresence: false}}}
	if _, err := Render(p, sign.OpSignImage, env); err == nil {
		t.Fatal("a device missing required physical presence must not satisfy the contract")
	}
}

func TestRender_OpVerbs(t *testing.T) {
	t.Setenv("SF_TEST_KEY", "/keys/cosign.key")
	p := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:SF_TEST_KEY"}
	for op, verb := range map[sign.Op]string{
		sign.OpSignImage: "sign",
		sign.OpAttest:    "attest",
		sign.OpSignBlob:  "sign-blob",
	} {
		args, err := Render(p, op, EnvForPlan(p))
		if err != nil {
			t.Fatalf("render %s: %v", op, err)
		}
		if args[0] != verb {
			t.Errorf("op %s → verb %q, want %q", op, args[0], verb)
		}
		// sign-blob is detached — no registry upload confirmation.
		if op == sign.OpSignBlob && slices.Contains(args, "--yes") {
			t.Errorf("sign-blob must not emit --yes: %v", args)
		}
	}
}

func TestEnvForPlan(t *testing.T) {
	t.Setenv("SF_TEST_KEY", "/k/cosign.key")
	t.Setenv("SF_KMS_REL", "hashivault://transit/keys/rel")

	if env := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:SF_TEST_KEY"}); len(env.Keys) != 1 || env.Keys[0].Path != "/k/cosign.key" {
		t.Errorf("key resolution: %+v", env.Keys)
	}
	if env := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassKMS, KMSRef: "rel"}); len(env.KMS) != 1 || env.KMS[0].URI != "hashivault://transit/keys/rel" {
		t.Errorf("kms resolution: %+v", env.KMS)
	}
	if env := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassOIDC, Identity: sign.IdentityConstraints{Issuer: "iss", Subject: "sub"}}); len(env.OIDC) != 1 || env.OIDC[0].Issuer != "iss" {
		t.Errorf("oidc resolution: %+v", env.OIDC)
	}
	// hardware witnesses are declared externally — EnvForPlan yields none.
	if env := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassHardware}); len(env.Keys)+len(env.KMS)+len(env.FIDO2) != 0 {
		t.Errorf("hardware EnvForPlan should be empty: %+v", env)
	}
	if env := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:SF_NOPE_XYZ"}); len(env.Keys) != 0 {
		t.Errorf("an unresolved key must yield no witness: %+v", env.Keys)
	}
}

func TestEnvForPlan_SigstoreDeployment(t *testing.T) {
	t.Setenv("SF_SIGSTORE_DOMAIN", "internal")
	t.Setenv("SF_SIGSTORE_FULCIO", "https://fulcio.internal.example")
	t.Setenv("SF_SIGSTORE_TRUSTED_ROOT", "/etc/sf/root.json")

	env := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassOIDC})
	d := env.Sigstore
	if d.Domain != "internal" || d.FulcioURL != "https://fulcio.internal.example" || d.TrustedRoot != "/etc/sf/root.json" {
		t.Fatalf("deployment not resolved from env: %+v", d)
	}
	if !d.Declared() {
		t.Errorf("a deployment with Fulcio/trusted-root must report Declared()")
	}
	// Issuer falls back to the profile identity when the env var is unset.
	env2 := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassOIDC, Identity: sign.IdentityConstraints{Issuer: "https://p"}})
	if env2.Sigstore.OIDCIssuer != "https://p" {
		t.Errorf("issuer should fall back to profile identity: %+v", env2.Sigstore)
	}
}

func TestEnvForPlan_PKCS11(t *testing.T) {
	const uri = "pkcs11:slot-id=0;id=%02;object=SIGN%20key?module-path=/usr/lib/libykcs11.so"
	t.Setenv("SF_PKCS11_RELEASE", uri)
	plan := sign.SignPlan{TrustClass: sign.ClassHardware, PKCS11Ref: "release", RequiresPhysicalPresence: true, RequiresNonExportableKey: true}
	env := EnvForPlan(plan)
	if len(env.PKCS11) != 1 || env.PKCS11[0].URI != uri {
		t.Fatalf("pkcs11 ref must bind a witness from SF_PKCS11_<REF>: %+v", env.PKCS11)
	}
	if !env.PKCS11[0].PhysicalPresence || !env.PKCS11[0].NonExportable {
		t.Errorf("a hardware-token witness must satisfy hardware assurance: %+v", env.PKCS11[0])
	}
	// An unresolved ref → no witness (the caller then falls back to FIDO2).
	if e := EnvForPlan(sign.SignPlan{TrustClass: sign.ClassHardware, PKCS11Ref: "missing"}); len(e.PKCS11) != 0 {
		t.Errorf("unresolved pkcs11 ref must yield no witness: %+v", e.PKCS11)
	}
}

func TestSigstoreDomain(t *testing.T) {
	// non-oidc → no trust domain.
	if got := SigstoreDomain(sign.SignPlan{TrustClass: sign.ClassKey}, Env{}); got != "" {
		t.Errorf("key class has no trust domain, got %q", got)
	}
	// oidc public (undeclared) → public-sigstore.
	if got := SigstoreDomain(sign.SignPlan{TrustClass: sign.ClassOIDC}, Env{}); got != "public-sigstore" {
		t.Errorf("undeclared oidc → public-sigstore, got %q", got)
	}
	// oidc declared with an explicit label → the label.
	env := Env{Sigstore: SigstoreDeployment{Domain: "internal", FulcioURL: "https://f.x"}}
	if got := SigstoreDomain(sign.SignPlan{TrustClass: sign.ClassOIDC}, env); got != "internal" {
		t.Errorf("explicit domain label, got %q", got)
	}
	// oidc declared, no label → derived from the Fulcio host.
	env2 := Env{Sigstore: SigstoreDeployment{FulcioURL: "https://fulcio.internal.example:8443/x"}}
	if got := SigstoreDomain(sign.SignPlan{TrustClass: sign.ClassOIDC}, env2); got != "fulcio.internal.example" {
		t.Errorf("derived host label, got %q", got)
	}
}
