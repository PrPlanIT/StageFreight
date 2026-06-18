package autosign

import (
	"slices"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
)

// The publisher contract is a faithful TRIPLE: declared intent (a SignPlan compiled
// from a profile) → emitted operation (Render argv) → recorded evidence
// (SigningContext.Evidence). Each layer PROJECTS the declared trust; none infers
// semantics from another. These tests pin that no projection silently drifts from the
// others — the exact class of bug that binary.go's dropped TrustDomain was (intent +
// operation correct, evidence projection dishonest). They are the executable form of
// "the publisher transcribes, it never interprets."

// kms + non_exportable: the realized KMS URI must reach argv, and non_exportable +
// class must reach the evidence — neither dropped, neither invented.
func TestTriple_KMS_NonExportable(t *testing.T) {
	t.Setenv("SF_KMS_RELEASE", "hashivault://release")
	plan := sign.Compile(&config.ResolvedSigningProfile{ID: "org-kms", Class: "kms", KMSRef: "release", NonExportable: true})
	env := cosign.EnvForPlan(plan)
	argv, err := cosign.Render(plan, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	ev := SigningContext{Plan: plan, Env: env, DoSign: true}.Evidence("2026-01-01T00:00:00Z")

	if !slices.Contains(argv, "hashivault://release") {
		t.Errorf("emitted argv lost the realized KMS URI: %v", argv)
	}
	if ev.TrustClass != "kms" || !ev.NonExportable {
		t.Errorf("recorded evidence drifted from declared (class=kms, non_exportable): %+v", ev)
	}
}

// key: the resolved key path must reach argv, and class + signer ref the evidence.
func TestTriple_Key(t *testing.T) {
	t.Setenv("SF_TEST_K", "/keys/cosign.key")
	plan := sign.Compile(&config.ResolvedSigningProfile{ID: "release-key", Class: "key", KeyRef: "env:SF_TEST_K"})
	env := cosign.EnvForPlan(plan)
	argv, err := cosign.Render(plan, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	ev := SigningContext{Plan: plan, Env: env, DoSign: true}.Evidence("2026-01-01T00:00:00Z")

	if !slices.Contains(argv, "/keys/cosign.key") {
		t.Errorf("emitted argv lost the resolved key path: %v", argv)
	}
	if ev.TrustClass != "key" || ev.SignerRef != "env:SF_TEST_K" {
		t.Errorf("recorded evidence drifted: %+v", ev)
	}
}

// oidc + self-hosted domain: the Fulcio URL + offline signing-config must reach argv,
// and transparency + trust domain the evidence.
func TestTriple_OIDC_SelfHosted(t *testing.T) {
	t.Setenv("SF_SIGSTORE_DOMAIN", "internal")
	t.Setenv("SF_SIGSTORE_FULCIO", "https://fulcio.internal.example")
	t.Setenv("SF_SIGSTORE_ISSUER", "https://id.internal.example")
	plan := sign.Compile(&config.ResolvedSigningProfile{ID: "keyless", Class: "oidc", OIDCIssuer: "https://id.internal.example"})
	env := cosign.EnvForPlan(plan)
	argv, err := cosign.Render(plan, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	ev := SigningContext{Plan: plan, Env: env, DoSign: true}.Evidence("2026-01-01T00:00:00Z")

	if !slices.Contains(argv, "https://fulcio.internal.example") || !slices.Contains(argv, "--use-signing-config=false") {
		t.Errorf("emitted argv did not point at the self-hosted Fulcio: %v", argv)
	}
	if ev.TrustClass != "oidc" || !ev.Transparency || ev.TrustDomain != "internal" {
		t.Errorf("recorded evidence drifted (oidc, transparency, trust_domain=internal): %+v", ev)
	}
}

// hardware + physical_presence + non_exportable: a FIDO2 witness → --sk, and both
// assurance properties must reach the evidence. Hardware witnesses are declared
// externally (EnvForPlan yields none), so the Env is supplied directly.
func TestTriple_Hardware_PresenceNonExportable(t *testing.T) {
	plan := sign.Compile(&config.ResolvedSigningProfile{ID: "maintainer", Class: "hardware", PhysicalPresence: true, NonExportable: true})
	env := cosign.Env{FIDO2: []cosign.FIDO2Device{{Principal: "yk-A", PhysicalPresence: true, NonExportable: true}}}
	argv, err := cosign.Render(plan, sign.OpSignImage, env)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	ev := SigningContext{Plan: plan, Env: env, DoSign: true}.Evidence("2026-01-01T00:00:00Z")

	if !slices.Contains(argv, "--sk") {
		t.Errorf("emitted argv did not select the FIDO2 token: %v", argv)
	}
	if ev.TrustClass != "hardware" || !ev.PhysicalPresence || !ev.NonExportable {
		t.Errorf("recorded evidence drifted (hardware, presence, non_exportable): %+v", ev)
	}
}
