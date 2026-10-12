package k8s

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// A cluster unlike dungeon in every name: what it declares decides tier, heading and
// order; undeclared namespaces fall back to generic signals or stay unresolved.
func TestDeclaredCategoriesDriveTierAndOrder(t *testing.T) {
	resolver := NewCategoryResolver([]config.InventoryCategory{
		{Name: "Kitchen", Tier: "app", Namespaces: []string{"pantry"}},
		{Name: "Plumbing", Tier: "platform", Namespaces: []string{"pipes"}},
		{Name: "Attic", Namespaces: []string{"rafters"}},
	})
	group := func(ns, name, kind string, routed bool) *appGroup {
		g := &appGroup{
			identity:  WorkloadIdentity{Key: AppKey{Namespace: ns, Identity: name}},
			workloads: []workloadInfo{{name: name, kind: kind, labels: map[string]string{}}},
		}
		if routed {
			g.routes = []ExposureRef{{Host: name + ".example"}}
		}
		return g
	}
	groups := map[AppKey]*appGroup{}
	for _, g := range []*appGroup{
		group("pantry", "toaster", "Deployment", false),         // declared app, no route
		group("pipes", "boiler", "Deployment", true),            // declared platform despite a route
		group("rafters", "lamp", "Deployment", true),            // declared section, tier inferred: routed
		group("rafters", "fan-controller", "Deployment", false), // declared section, tier inferred: name
		group("garage", "workbench", "Deployment", true),        // undeclared, routed
		group("garage", "shelf", "StatefulSet", false),          // undeclared, no signal
	} {
		groups[g.identity.Key] = g
	}

	recs := buildRecords(groups, &Catalog{}, resolver, nil, config.ExposureRules{})
	got := map[string]AppRecord{}
	for _, r := range recs {
		got[r.Key.Identity] = r
	}
	for name, want := range map[string]struct {
		tier     Tier
		category string
	}{
		"toaster":        {TierApp, "Kitchen"},
		"boiler":         {TierPlatform, "Plumbing"},
		"lamp":           {TierApp, "Attic"},
		"fan-controller": {TierPlatform, "Attic"},
		"workbench":      {TierApp, Uncategorized},
		"shelf":          {TierUnresolved, Uncategorized},
	} {
		if r := got[name]; r.Tier != want.tier || r.Category != want.category {
			t.Errorf("%s: got tier=%q category=%q, want tier=%q category=%q", name, r.Tier, r.Category, want.tier, want.category)
		}
	}

	apps := []AppRecord{got["workbench"], got["lamp"], got["toaster"]}
	sortRecords(apps, resolver)
	if apps[0].Key.Identity != "toaster" || apps[1].Key.Identity != "lamp" || apps[2].Key.Identity != "workbench" {
		t.Errorf("sections follow declaration order, undeclared last: got %s, %s, %s",
			apps[0].Key.Identity, apps[1].Key.Identity, apps[2].Key.Identity)
	}
}
