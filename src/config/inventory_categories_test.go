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

func TestValidateExposureRules(t *testing.T) {
	cfg := &Config{GitOps: OrderedClusters{{Name: "lab", Exposure: ExposureRules{Rules: []ExposureRule{
		{Level: "public", Endpoints: []string{"10.0.0.1"}, CIDRs: []string{"10.0.0.0/33"}, Ports: []int{0}, ServiceTypes: []string{"Headless"}},
		{Level: "intranet", IngressClasses: []string{"nginx"}},
	}}}}}
	errs := strings.Join(validateExposureRules(cfg), "\n")
	for _, want := range []string{
		`gitops.lab.exposure[0].level: unknown level "public"`,
		`gitops.lab.exposure[0].endpoints: "10.0.0.1" is not ip:port`,
		`gitops.lab.exposure[0].cidrs: "10.0.0.0/33" is not a CIDR`,
		`gitops.lab.exposure[0].ports: 0 is not a port`,
		`gitops.lab.exposure[0].service_types: unknown type "Headless"`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "exposure[1]") {
		t.Errorf("a valid ingress_classes rule must pass:\n%s", errs)
	}
}
