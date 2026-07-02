package cmd

import (
	"bytes"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/gitops"
)

// TestRenderFluxValidationPreview reconstructs the real dungeon audition scenario
// (one crd-catalog advisory on the Vault CR, seven kinds with no published schema)
// and renders it so the tiered layout can be eyeballed and asserted.
func TestRenderFluxValidationPreview(t *testing.T) {
	verdicts := map[gitops.KustomizationKey]gitops.Verdict{}
	for i, n := range []string{
		"infra-controllers", "infra-configs", "infra-namespaces", "apps",
		"monitoring", "storage", "networking", "security",
		"backup", "mail", "media", "games",
	} {
		key := gitops.KustomizationKey{Namespace: "flux-system", Name: n}
		v := gitops.Verdict{Status: gitops.Pass}
		if i == 1 { // infra-configs carries the Vault advisory
			v = gitops.Verdict{
				Status: gitops.Warn,
				Findings: []gitops.Finding{{
					Severity: gitops.Warn,
					Source:   "crd-catalog",
					Message:  "Vault/vault (v1alpha1): problem validating schema...",
					Schema: &gitops.SchemaFinding{
						Kind: "Vault", Name: "vault", Version: "v1alpha1",
						Field:     "spec.vaultContainerSpec.name",
						Rule:      "required by schema, not set",
						SchemaURL: "https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/vault.banzaicloud.com/vault_v1alpha1.json",
						Raw:       "problem validating schema. Check JSON formatting: jsonschema: '/spec/vaultContainerSpec' does not validate with https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/vault.banzaicloud.com/vault_v1alpha1.json#/properties/spec/properties/vaultContainerSpec/required: missing properties: 'name'",
					},
				}},
			}
		}
		verdicts[key] = v
	}

	meta := &gitops.ValidationMeta{
		Roots:          12,
		KustomizeVer:   "5.5.0",
		KubeconformVer: "0.6.7",
		Validated:      map[string]int{"Deployment": 900, "Service": 700, "ConfigMap": 405},
		NoSchema: map[string]int{
			"CustomResourceDefinition": 13, "ExternalSecret": 6, "HelmRelease": 3,
			"HTTPRoute": 2, "CephCluster": 1, "Certificate": 1, "CiliumNetworkPolicy": 1,
		},
	}

	var buf bytes.Buffer
	renderFluxValidation(&buf, time.Now().Add(-33900*time.Millisecond), verdicts, meta)
	out := buf.String()
	t.Logf("\n%s", out)

	for _, want := range []string{
		"authoritative", "12/12 kustomizations valid",
		"heuristic", "may be stricter than your operator",
		"Vault/vault", "spec.vaultContainerSpec.name", "required by schema, not set",
		"datreeio CRD-catalog · vault.banzaicloud.com/vault_v1alpha1",
		"schema unavailable", "no published schema", "CRD·13",
		"PASS", "12/12 valid", "1 advisory", "27 schema-unavailable",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("rendered output missing %q", want)
		}
	}
	// The misleading raw wrapper must NOT reach the primary surface.
	if bytes.Contains([]byte(out), []byte("Check JSON formatting")) {
		t.Errorf("primary surface leaked raw validator wrapper 'Check JSON formatting'")
	}
}
