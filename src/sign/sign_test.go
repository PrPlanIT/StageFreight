package sign

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func bptr(b bool) *bool { return &b }

// Compile is the total lowering of a resolved profile to its trust contract.
func TestCompile_TrustContract(t *testing.T) {
	cases := []struct {
		name string
		in   config.ResolvedSigningProfile
		want SignPlan
	}{
		{
			"oidc defaults transparency on",
			config.ResolvedSigningProfile{Class: "oidc", OIDCIssuer: "https://iss", OIDCIdentity: "ci@x"},
			SignPlan{TrustClass: ClassOIDC, TransparencyRequired: true, Identity: IdentityConstraints{Issuer: "https://iss", Subject: "ci@x"}},
		},
		{
			"key defaults transparency off, carries logical ref",
			config.ResolvedSigningProfile{Class: "key", KeyRef: "env:COSIGN_KEY"},
			SignPlan{TrustClass: ClassKey, KeyRef: "env:COSIGN_KEY"},
		},
		{
			"hardware carries assurance requirements",
			config.ResolvedSigningProfile{Class: "hardware", PhysicalPresence: true, NonExportable: true, TransparencyLog: bptr(true)},
			SignPlan{TrustClass: ClassHardware, RequiresPhysicalPresence: true, RequiresNonExportableKey: true, TransparencyRequired: true},
		},
		{
			"transparency override beats oidc default",
			config.ResolvedSigningProfile{Class: "oidc", TransparencyLog: bptr(false)},
			SignPlan{TrustClass: ClassOIDC, TransparencyRequired: false},
		},
		{
			"kms carries logical ref, no mechanism leaks in",
			config.ResolvedSigningProfile{Class: "kms", KMSRef: "release-signing-key"},
			SignPlan{TrustClass: ClassKMS, KMSRef: "release-signing-key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compile(&tc.in); got != tc.want {
				t.Errorf("Compile = %+v\n      want %+v", got, tc.want)
			}
		})
	}
}

// Enabled preserves no-key-no-signing for the legacy key path; non-key classes
// are enabled because they exist only when explicitly configured.
func TestEnabled_NoKeyNoSigning(t *testing.T) {
	t.Setenv("COSIGN_KEY", "")
	if Enabled(SignPlan{TrustClass: ClassKey, KeyRef: "env:COSIGN_KEY"}) {
		t.Error("key plan with unresolved key must be disabled")
	}
	t.Setenv("COSIGN_KEY", "/path/to/key")
	if !Enabled(SignPlan{TrustClass: ClassKey, KeyRef: "env:COSIGN_KEY"}) {
		t.Error("key plan with resolved env key must be enabled")
	}
	if !Enabled(SignPlan{TrustClass: ClassOIDC}) {
		t.Error("oidc plan must be enabled")
	}
}

func TestCompile_KMSCarriesNonExportable(t *testing.T) {
	plan := Compile(&config.ResolvedSigningProfile{Class: "kms", KMSRef: "rel", NonExportable: true})
	if !plan.RequiresNonExportableKey {
		t.Error("a kms profile asserting non_exportable must carry it (a KMS/Vault key never leaves the service)")
	}
	if plan.RequiresPhysicalPresence {
		t.Error("kms must not carry physical presence")
	}
}
