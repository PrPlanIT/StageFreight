package k8s

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		group("rafters", "beam", "StatefulSet", false),          // declared section, no signal: platform
		group("garage", "workbench", "Deployment", true),        // undeclared, routed
		group("garage", "shelf", "StatefulSet", false),          // undeclared, no signal
	} {
		groups[g.identity.Key] = g
	}

	recs := buildRecords(groups, &Catalog{}, resolver, nil, config.ExposureRules{}, config.GroupSource{})
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
		"beam":           {TierPlatform, "Attic"},
		"workbench":      {TierApp, Uncategorized},
		"shelf":          {TierUnresolved, Uncategorized},
	} {
		if r := got[name]; r.Tier != want.tier || r.Category != want.category {
			t.Errorf("%s: got tier=%q category=%q, want tier=%q category=%q", name, r.Tier, r.Category, want.tier, want.category)
		}
	}

	apps := []AppRecord{got["workbench"], got["lamp"], got["toaster"]}
	sortRecords(apps)
	if apps[0].Key.Identity != "lamp" || apps[1].Key.Identity != "toaster" || apps[2].Key.Identity != "workbench" {
		t.Errorf("sections sort alphabetically, Uncategorized last: got %s, %s, %s",
			apps[0].Key.Identity, apps[1].Key.Identity, apps[2].Key.Identity)
	}
}

func TestListedCountsEveryRunningSection(t *testing.T) {
	r := &DiscoveryResult{Apps: make([]AppRecord, 3), Platform: make([]AppRecord, 2), Unresolved: make([]AppRecord, 1)}
	if got := r.Listed(); got != 6 {
		t.Errorf("got %d, want 6", got)
	}
}

// The heading is the section's name as declared; the namespaces show in each app's details.
func TestCategoryHeadingIsTheDeclaredName(t *testing.T) {
	app := func(ns, name string) AppRecord {
		return AppRecord{Key: AppKey{Namespace: ns, Identity: name}, FriendlyName: name, Category: "Observability", Status: StatusHealthy}
	}
	out := RenderOverview(&DiscoveryResult{Cluster: "lab", Apps: []AppRecord{app("watchtower", "grafana"), app("beacon", "netalert")}})
	if !strings.Contains(out, "### Observability\n") {
		t.Errorf("heading should be the category name alone:\n%s", out)
	}
}

func TestDeclaredGroupReadsAnnotationFieldOrLabel(t *testing.T) {
	meta := metav1.ObjectMeta{
		Annotations: map[string]string{"gatus.example/endpoint": "group: \"Game Servers\"\nurl: http://x\n"},
		Labels:      map[string]string{"team.example/group": "Media"},
	}
	if got := declaredGroup(meta, config.GroupSource{Annotation: "gatus.example/endpoint", Field: "group"}); got != "Game Servers" {
		t.Errorf("annotation field: got %q", got)
	}
	if got := declaredGroup(meta, config.GroupSource{Label: "team.example/group"}); got != "Media" {
		t.Errorf("label: got %q", got)
	}
	if got := declaredGroup(meta, config.GroupSource{Annotation: "absent"}); got != "" {
		t.Errorf("absent: got %q", got)
	}
}

// A declared group places an app outright: an app, under that group, even in a namespace
// the cluster declares nothing about and with no route. Without one, the namespace
// declaration still applies.
func TestDeclaredGroupPlacesTheApp(t *testing.T) {
	src := config.GroupSource{Annotation: "gatus.example/endpoint", Field: "group"}
	svc := func(name, group string) corev1.Service {
		s := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "arcade"}}
		if group != "" {
			s.Annotations = map[string]string{"gatus.example/endpoint": "group: " + group + "\n"}
		}
		return s
	}
	groups := map[AppKey]*appGroup{}
	for _, g := range []*appGroup{
		{identity: WorkloadIdentity{Key: AppKey{Namespace: "arcade", Identity: "pinball"}},
			workloads: []workloadInfo{{name: "pinball", kind: "StatefulSet", labels: map[string]string{}}},
			services:  []corev1.Service{svc("pinball", "Game Servers")}},
		{identity: WorkloadIdentity{Key: AppKey{Namespace: "pantry", Identity: "kettle"}},
			workloads: []workloadInfo{{name: "kettle", kind: "StatefulSet", labels: map[string]string{}}},
			services:  []corev1.Service{svc("kettle", "")}},
	} {
		groups[g.identity.Key] = g
	}
	resolver := NewCategoryResolver([]config.InventoryCategory{{Name: "Kitchen", Tier: "app", Namespaces: []string{"pantry"}}})
	got := map[string]AppRecord{}
	for _, r := range buildRecords(groups, &Catalog{}, resolver, nil, config.ExposureRules{}, src) {
		got[r.Key.Identity] = r
	}
	if r := got["pinball"]; r.Tier != TierApp || r.Category != "Game Servers" {
		t.Errorf("grouped app: got tier=%q category=%q", r.Tier, r.Category)
	}
	if r := got["kettle"]; r.Tier != TierApp || r.Category != "Kitchen" {
		t.Errorf("ungrouped app falls back to its namespace: got tier=%q category=%q", r.Tier, r.Category)
	}
}
