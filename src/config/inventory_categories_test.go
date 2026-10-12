package config

import (
	"strings"
	"testing"
)

func TestValidateInventoryCategories(t *testing.T) {
	cfg := &Config{GitOps: OrderedClusters{{Name: "lab", Categories: []InventoryCategory{
		{Name: "Media", Tier: "app", Namespaces: []string{"films"}},
		{Name: "Tools", Tier: "gadget", Namespaces: []string{"films"}},
		{Tier: "platform"},
	}}}}
	errs := strings.Join(validateInventoryCategories(cfg), "\n")
	for _, want := range []string{
		`gitops.lab.categories[1].tier: unknown tier "gadget"`,
		`gitops.lab.categories[1].namespaces: "films" is already in category "Media"`,
		`gitops.lab.categories[2].name: required`,
		`gitops.lab.categories[2].namespaces: required`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
}
