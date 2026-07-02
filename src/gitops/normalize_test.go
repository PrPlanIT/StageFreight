package gitops

import "testing"

func TestParseSchemaViolation(t *testing.T) {
	const vaultRaw = "problem validating schema. Check JSON formatting: jsonschema: '/spec/vaultContainerSpec' does not validate with https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/vault.banzaicloud.com/vault_v1alpha1.json#/properties/spec/properties/vaultContainerSpec/required: missing properties: 'name'"

	t.Run("required/missing property points field at the missing name", func(t *testing.T) {
		sf := parseSchemaViolation("Vault", "vault", "v1alpha1", vaultRaw)
		if !sf.Parsed() {
			t.Fatalf("expected Parsed()=true")
		}
		if sf.Field != "spec.vaultContainerSpec.name" {
			t.Errorf("Field = %q, want spec.vaultContainerSpec.name", sf.Field)
		}
		if sf.Rule != "required by schema, not set" {
			t.Errorf("Rule = %q, want 'required by schema, not set'", sf.Rule)
		}
		if sf.SchemaURL != "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/vault.banzaicloud.com/vault_v1alpha1.json" {
			t.Errorf("SchemaURL = %q", sf.SchemaURL)
		}
		if sf.Raw == "" || sf.Kind != "Vault" || sf.Name != "vault" {
			t.Errorf("identity/raw not preserved: %+v", sf)
		}
	})

	t.Run("generic violation kept verbatim, not synthesized", func(t *testing.T) {
		raw := "jsonschema: '/spec/replicas' does not validate with https://x/schema.json#/properties/spec/properties/replicas/type: expected integer, but got string"
		sf := parseSchemaViolation("Foo", "foo", "v1", raw)
		if sf.Field != "spec.replicas" {
			t.Errorf("Field = %q, want spec.replicas", sf.Field)
		}
		if sf.Rule != "expected integer, but got string" {
			t.Errorf("Rule = %q, want verbatim violation", sf.Rule)
		}
		if sf.SchemaURL != "https://x/schema.json" {
			t.Errorf("SchemaURL = %q", sf.SchemaURL)
		}
	})

	t.Run("unparseable message degrades to raw, never invents meaning", func(t *testing.T) {
		raw := "some totally different validator failure text"
		sf := parseSchemaViolation("Bar", "bar", "v1", raw)
		if sf.Parsed() {
			t.Errorf("expected Parsed()=false for unrecognized text, got Field=%q Rule=%q", sf.Field, sf.Rule)
		}
		if sf.Raw != raw {
			t.Errorf("Raw = %q, want verbatim", sf.Raw)
		}
	})

	t.Run("multiple missing properties", func(t *testing.T) {
		raw := "jsonschema: '/spec' does not validate with https://x#/properties/spec/required: missing properties: 'a', 'b'"
		sf := parseSchemaViolation("Baz", "baz", "v1", raw)
		if sf.Field != "spec.a, spec.b" {
			t.Errorf("Field = %q, want 'spec.a, spec.b'", sf.Field)
		}
	})
}
