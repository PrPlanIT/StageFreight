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
	if slices.Contains(args, "--tlog-upload=false") {
		t.Errorf("transparency-required render must not disable the tlog: %v", args)
	}
}

func TestRender_KMSClass(t *testing.T) {
	t.Setenv("SF_SIGN_KMS_RELEASE", "awskms://alias/release")
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
	t.Setenv("SF_SIGN_KMS_REL", "hashivault://transit/keys/rel")

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
