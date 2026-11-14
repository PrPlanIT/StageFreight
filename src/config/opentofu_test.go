package config

import (
	"slices"
	"strings"
	"testing"
)

const openTofuFixture = `
opentofu:
  backend: terraform
  credentials: oracle-site
  roots:
    oracle-site:
      path: opentofu/oracle-site
      converge: true
      workspace: production
      var_files: [production.tfvars]
    teardown:
      path: opentofu/teardown
      converge: false
      required: false
`

// TestOpenTofuConfig_RoundTrip pins the library model: strict decode, document order,
// id stamping from the map key, and the converge/runbook split.
func TestOpenTofuConfig_RoundTrip(t *testing.T) {
	cfg := decodeAnsibleFixture(t, openTofuFixture)
	o := cfg.OpenTofu

	if o.Backend != OpenTofuBackendTerraform {
		t.Errorf("backend: %q", o.Backend)
	}
	if o.EnvPrefix() != "ORACLE_SITE" {
		t.Errorf("env prefix: %q", o.EnvPrefix())
	}
	if len(o.Roots) != 2 || o.Roots[0].ID != "oracle-site" || o.Roots[1].ID != "teardown" {
		t.Fatalf("root order/ids: %+v", o.Roots)
	}
	r := o.Roots[0]
	if r.Path != "opentofu/oracle-site" || r.Workspace != "production" || !slices.Equal(r.VarFiles, []string{"production.tfvars"}) || !r.IsRequired() {
		t.Errorf("root fields: %+v", r)
	}
	if o.Roots[1].IsRequired() {
		t.Error("required: false must be advisory")
	}
	if !o.HasConvergeRoots() {
		t.Error("converge presence gate must be true")
	}
	if got := o.ConvergeRoots(); len(got) != 1 || got[0].ID != "oracle-site" {
		t.Errorf("converge set: %+v", got)
	}
	if errs := validateOpenTofu(&cfg); len(errs) != 0 {
		t.Errorf("valid config rejected: %v", errs)
	}
}

func TestOpenTofuConfig_DefaultBackend(t *testing.T) {
	cfg := decodeAnsibleFixture(t, "opentofu:\n  credentials: OCI\n  roots:\n    a:\n      path: x\n      converge: true\n")
	if cfg.OpenTofu.Backend != OpenTofuBackendOpenTofu {
		t.Errorf("default backend = %q, want opentofu", cfg.OpenTofu.Backend)
	}
}

func TestValidateOpenTofu(t *testing.T) {
	cfg := decodeAnsibleFixture(t, `
opentofu:
  backend: pulumi
  roots:
    a:
      converge: true
      var_files: [""]
`)
	errs := strings.Join(validateOpenTofu(&cfg), "\n")
	for _, want := range []string{
		`opentofu.backend: "pulumi"`,
		"opentofu.roots.a.path: required",
		"opentofu.roots.a.var_files[0]: empty",
		"opentofu.credentials: required",
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}

	absent := *defaults()
	if errs := validateOpenTofu(&absent); errs != nil {
		t.Errorf("absent subsystem must validate clean, got %v", errs)
	}
}

// TestOpenTofuCredentialEnv: <PREFIX>_<NAME> reaches the binary as <NAME>; other
// variables and empty values do not.
func TestOpenTofuCredentialEnv(t *testing.T) {
	o := OpenTofuConfig{Credentials: "oci"}
	got := o.CredentialEnv([]string{
		"OCI_TF_VAR_tenancy_ocid=ocid1.tenancy",
		"OCI_AWS_ACCESS_KEY_ID=key",
		"OCI_EMPTY=",
		"OCIX_OTHER=no",
		"PATH=/bin",
	})
	want := []string{"TF_VAR_tenancy_ocid=ocid1.tenancy", "AWS_ACCESS_KEY_ID=key"}
	if !slices.Equal(got, want) {
		t.Errorf("CredentialEnv = %v, want %v", got, want)
	}
	if (OpenTofuConfig{}).CredentialEnv([]string{"OCI_X=1"}) != nil {
		t.Error("no credentials declared must yield nothing")
	}
}
