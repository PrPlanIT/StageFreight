package cosign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/sign"
)

func TestRender_KeyClass(t *testing.T) {
	t.Setenv("SF_TEST_KEY", "/keys/cosign.key")
	p := sign.SignPlan{TrustClass: sign.ClassKey, KeyRef: "env:SF_TEST_KEY"}
	env := EnvForPlan(p)
	env.SigningConfig = "/tmp/offline.json"
	args, err := Render(p, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := []string{"sign", "--key", "/keys/cosign.key", "--signing-config", "/tmp/offline.json", "--yes"}
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
	args, err := Render(p, sign.OpSignImage, withConfig(Env{}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if slices.Contains(args, "--key") {
		t.Errorf("keyless render must not emit --key: %v", args)
	}
	if !flagHasValue(args, "--signing-config", "/tmp/offline.json") || slices.Contains(args, "--use-signing-config=true") || slices.Contains(args, "--new-bundle-format=true") {
		t.Errorf("services come only from SF's signing config, with no deprecated flag: %v", args)
	}
	if slices.Contains(args, "--tlog-upload=false") {
		t.Errorf("transparency-required render must not disable the tlog: %v", args)
	}
}

// A DECLARED (self-hosted) Sigstore deployment names its own services in a signing config
// (public Fulcio won't trust a self-hosted issuer); the issuer, identity token and trusted
// root still travel as flags.
func TestRender_OIDC_SelfHostedDeployment(t *testing.T) {
	t.Setenv("SF_SIGSTORE_DOMAIN", "internal")
	t.Setenv("SF_SIGSTORE_FULCIO", "https://fulcio.internal.example")
	t.Setenv("SF_SIGSTORE_REKOR", "https://rekor.internal.example")
	t.Setenv("SF_SIGSTORE_ISSUER", "https://id.internal.example")
	t.Setenv("SF_SIGSTORE_TRUSTED_ROOT", "/etc/sf/trusted-root.json")
	t.Setenv("SF_SIGSTORE_IDENTITY_TOKEN", "/run/secrets/oidc-token")

	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true}
	args, err := Render(p, sign.OpSignImage, withConfig(EnvForPlan(p)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, pair := range [][2]string{
		{"--oidc-issuer", "https://id.internal.example"},
		{"--trusted-root", "/etc/sf/trusted-root.json"},
		{"--identity-token", "/run/secrets/oidc-token"},
		{"--signing-config", "/tmp/offline.json"},
	} {
		if !flagHasValue(args, pair[0], pair[1]) {
			t.Errorf("expected %s %s in %v", pair[0], pair[1], args)
		}
	}
	for _, gone := range []string{"--fulcio-url", "--rekor-url", "--use-signing-config=false", "--use-signing-config=true", "--tlog-upload=false", "--key"} {
		if slices.Contains(args, gone) {
			t.Errorf("%s must not be emitted: %v", gone, args)
		}
	}
}

// Self-hosted WITHOUT transparency: the signing config carries no Rekor, and no
// deprecated flag is emitted.
func TestRender_OIDC_SelfHostedNoTransparency(t *testing.T) {
	t.Setenv("SF_SIGSTORE_FULCIO", "https://fulcio.internal.example")
	t.Setenv("SF_SIGSTORE_REKOR", "https://rekor.internal.example")
	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: false}
	args, err := Render(p, sign.OpSignBlob, withConfig(EnvForPlan(p)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if slices.Contains(args, "--rekor-url") || slices.Contains(args, "--tlog-upload=false") || !slices.Contains(args, "--signing-config") {
		t.Errorf("want only the signing config: %v", args)
	}
	data, err := SigningConfigJSON(context.Background(), p, EnvForPlan(p).Sigstore)
	if err != nil || strings.Contains(string(data), "rekor.internal.example") {
		t.Errorf("no-transparency config must name no Rekor: %v %s", err, data)
	}
}

// The issuer falls back to the profile's declared identity issuer when the env var
// is unset — a profile stays portable while an operator supplies the deployment.
func TestRender_OIDC_IssuerFallsBackToProfileIdentity(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true,
		Identity: sign.IdentityConstraints{Issuer: "https://gitlab.example/oauth"}}
	args, err := Render(p, sign.OpSignImage, withConfig(EnvForPlan(p)))
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
	args, err := Render(p, sign.OpSignImage, withConfig(EnvForPlan(p)))
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
	args, err := Render(p, sign.OpSignImage, withConfig(env))
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
	args, err := Render(p, sign.OpSignImage, withConfig(env))
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
	args, err := Render(p, sign.OpSignImage, withConfig(env))
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
		args, err := Render(p, op, withConfig(EnvForPlan(p)))
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
	if got := SigstoreDomain(sign.SignPlan{TrustClass: sign.ClassKMS, TransparencyRequired: true}, Env{}); got != "public-sigstore" {
		t.Errorf("a logged KMS signature → public-sigstore, got %q", got)
	}
	if got := SigstoreDomain(sign.SignPlan{TrustClass: sign.ClassKMS, TransparencyRequired: true}, Env{Sigstore: SigstoreDeployment{RekorURL: "https://rekor.internal.example/"}}); got != "rekor.internal.example" {
		t.Errorf("a KMS signature logged in a declared deployment → its Rekor host, got %q", got)
	}
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

// Without a transparency log every signature by a key, KMS or hardware signer, blob
// included, passes a signing config listing no services; none of cosign's deprecated
// flags is emitted.
func TestRender_OfflineSigningConfig(t *testing.T) {
	t.Setenv("SF_KMS_TENANT", "hashivault://acme")
	p := sign.SignPlan{TrustClass: sign.ClassKMS, KMSRef: "tenant"}
	env := EnvForPlan(p)

	if _, err := Render(p, sign.OpSignImage, env); err == nil || !strings.Contains(err.Error(), "signing config") {
		t.Errorf("a signature needing a config must be refused without one: %v", err)
	}
	env.SigningConfig = "/tmp/offline.json"
	for _, op := range []sign.Op{sign.OpSignImage, sign.OpAttest, sign.OpSignBlob} {
		args, err := Render(p, op, env)
		if err != nil {
			t.Fatal(err)
		}
		if !flagHasValue(args, "--signing-config", "/tmp/offline.json") || slices.Contains(args, "--tlog-upload=false") || slices.Contains(args, "--use-signing-config=false") {
			t.Errorf("op %v: want only the signing config, got %v", op, args)
		}
	}

	p.TransparencyRequired = true
	if _, err := Render(p, sign.OpSignImage, Env{KMS: env.KMS}); err == nil {
		t.Error("a logged signature must be refused without a signing config")
	}
	args, err := Render(p, sign.OpSignImage, env)
	if err != nil || !flagHasValue(args, "--signing-config", "/tmp/offline.json") || slices.Contains(args, "--new-bundle-format=true") {
		t.Errorf("transparency path: %v %v", args, err)
	}
}

// The executor writes the plan's signing config and removes it afterwards.
func TestWithSigningConfig(t *testing.T) {
	p := sign.SignPlan{TrustClass: sign.ClassKey}
	env, cleanup, err := withSigningConfig(context.Background(), p, Env{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(env.SigningConfig)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil || cfg["mediaType"] != "application/vnd.dev.sigstore.signingconfig.v0.2+json" {
		t.Errorf("not a signing config: %v %s", err, data)
	}
	for _, k := range []string{"caUrls", "rekorTlogUrls", "tsaUrls"} {
		if v, ok := cfg[k].([]any); !ok || len(v) != 0 {
			t.Errorf("%s must list no services: %v", k, cfg[k])
		}
	}
	cleanup()
	if _, err := os.Stat(env.SigningConfig); !os.IsNotExist(err) {
		t.Error("cleanup must remove the config")
	}
	published := stubPublicSigningConfig(t, []byte(`{"published":true}`), nil)
	env, cleanup, err = withSigningConfig(context.Background(), sign.SignPlan{TrustClass: sign.ClassKMS, TransparencyRequired: true}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if data, _ := os.ReadFile(env.SigningConfig); string(data) != string(published) {
		t.Errorf("a signature logged in public Sigstore uses its published config, got %s", data)
	}
	stubPublicSigningConfig(t, nil, errors.New("TUF unreachable"))
	if _, _, err := withSigningConfig(context.Background(), sign.SignPlan{TrustClass: sign.ClassKMS, TransparencyRequired: true}, Env{}); err == nil {
		t.Error("without a verified published config the signature must not be made")
	}
}

// stubPublicSigningConfig replaces the TUF fetch of public Sigstore's config for one test.
func stubPublicSigningConfig(t *testing.T, data []byte, err error) []byte {
	t.Helper()
	orig := publicSigningConfig
	publicSigningConfig = func(context.Context) ([]byte, error) { return data, err }
	t.Cleanup(func() { publicSigningConfig = orig })
	return data
}

// Public Sigstore's published configs, as its TUF repository serves them: the default
// names only Rekor v1, the Rekor v2 config names log2025-1 first.
func TestSelectPublicSigningConfig(t *testing.T) {
	v1, err := os.ReadFile("testdata/signing_config.v0.2.json")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := os.ReadFile("testdata/signing_config_rekor_v2.v0.2.json")
	if err != nil {
		t.Fatal(err)
	}
	serve := func(targets map[string][]byte) func(string) ([]byte, error) {
		return func(name string) ([]byte, error) {
			if d, ok := targets[name]; ok {
				return d, nil
			}
			return nil, fmt.Errorf("target %s not found", name)
		}
	}

	got, err := selectPublicSigningConfig(serve(map[string][]byte{publicConfigTarget: v1, publicRekorV2Target: v2}))
	if err != nil || string(got) != string(v2) {
		t.Errorf("while the default names only Rekor v1, the Rekor v2 config applies: %v", err)
	}
	got, err = selectPublicSigningConfig(serve(map[string][]byte{publicConfigTarget: v2}))
	if err != nil || string(got) != string(v2) {
		t.Errorf("a default that names Rekor v2 applies as published: %v", err)
	}
	if _, err := selectPublicSigningConfig(serve(map[string][]byte{publicConfigTarget: v1})); err == nil {
		t.Error("no Rekor v2 config published: refuse, never fall back to v1 silently")
	}
	if _, err := selectPublicSigningConfig(serve(map[string][]byte{publicConfigTarget: v1, publicRekorV2Target: v1})); err == nil {
		t.Error("a Rekor v2 config naming no v2 log must be refused")
	}
	if _, err := selectPublicSigningConfig(serve(map[string][]byte{publicConfigTarget: []byte(`{"mediaType":"x"}`)})); err == nil {
		t.Error("a document that is not a signing config must be refused")
	}
	if _, err := selectPublicSigningConfig(serve(nil)); err == nil {
		t.Error("an unreachable TUF repository must be refused")
	}
}

// A keyless config names the deployment's services, or public Sigstore's when none is
// declared; a declared deployment that must log but names no Rekor is refused.
func TestSigningConfigJSON(t *testing.T) {
	type cfg struct {
		CA    []struct{ URL string } `json:"caUrls"`
		OIDC  []struct{ URL string } `json:"oidcUrls"`
		Rekor []struct{ URL string } `json:"rekorTlogUrls"`
	}
	parse := func(p sign.SignPlan, d SigstoreDeployment) (cfg, error) {
		var c cfg
		data, err := SigningConfigJSON(context.Background(), p, d)
		if err != nil {
			return c, err
		}
		return c, json.Unmarshal(data, &c)
	}
	declared := SigstoreDeployment{FulcioURL: "https://fulcio.x", RekorURL: "https://rekor.x", OIDCIssuer: "https://id.x"}
	c, err := parse(sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true}, declared)
	if err != nil || len(c.CA) != 1 || c.CA[0].URL != "https://fulcio.x" || len(c.Rekor) != 1 || c.Rekor[0].URL != "https://rekor.x" || len(c.OIDC) != 1 {
		t.Errorf("declared keyless: %+v %v", c, err)
	}
	if _, err := parse(sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true}, SigstoreDeployment{FulcioURL: "https://fulcio.x"}); err == nil {
		t.Error("a declared deployment that must log but names no Rekor must be refused")
	}
	c, err = parse(sign.SignPlan{TrustClass: sign.ClassOIDC}, SigstoreDeployment{})
	if err != nil || len(c.CA) != 1 || c.CA[0].URL != publicFulcio || len(c.Rekor) != 0 {
		t.Errorf("public keyless without transparency: %+v %v", c, err)
	}
	// A key or KMS signature that must log goes to the declared deployment's Rekor, never public Sigstore's.
	c, err = parse(sign.SignPlan{TrustClass: sign.ClassKMS, TransparencyRequired: true}, declared)
	if err != nil || len(c.CA) != 0 || len(c.Rekor) != 1 || c.Rekor[0].URL != "https://rekor.x" {
		t.Errorf("declared KMS with transparency: %+v %v", c, err)
	}
	stubPublicSigningConfig(t, []byte(`{"published":true}`), nil)
	if data, err := SigningConfigJSON(context.Background(), sign.SignPlan{TrustClass: sign.ClassKMS, TransparencyRequired: true}, SigstoreDeployment{}); err != nil || string(data) != `{"published":true}` {
		t.Errorf("public KMS with transparency uses the published config: %s %v", data, err)
	}
}

// withConfig gives an env the signing config the executor writes.
func withConfig(env Env) Env {
	env.SigningConfig = "/tmp/offline.json"
	return env
}

// A keyless signature is verified against its declared identity and the deployment's trusted
// root; the log is skipped only when the plan does not require it.
func TestVerifyArgs_Keyless(t *testing.T) {
	d := SigstoreDeployment{FulcioURL: "https://fulcio.x", RekorURL: "https://rekor.x", TrustedRoot: "/root.json"}
	p := sign.SignPlan{TrustClass: sign.ClassOIDC, TransparencyRequired: true,
		Identity: sign.IdentityConstraints{Issuer: "https://id.x", Subject: "ci@x"}}
	args, cleanup, err := verifyArgs(context.Background(), "", nil, p, Env{Sigstore: d}, "verify")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if !flagHasValue(args, "--certificate-identity", "ci@x") || !flagHasValue(args, "--certificate-oidc-issuer", "https://id.x") ||
		!flagHasValue(args, "--trusted-root", "/root.json") || slices.Contains(args, "--insecure-ignore-tlog=true") {
		t.Errorf("logged keyless verify: %v", args)
	}

	p = sign.SignPlan{TrustClass: sign.ClassOIDC}
	args, cleanup, err = verifyArgs(context.Background(), "", nil, p, Env{}, "verify")
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	if !flagHasValue(args, "--certificate-identity-regexp", ".*") || !flagHasValue(args, "--certificate-oidc-issuer-regexp", ".*") ||
		!slices.Contains(args, "--insecure-ignore-tlog=true") {
		t.Errorf("an open identity is matched as any, and an unlogged signature skips the log: %v", args)
	}
}
