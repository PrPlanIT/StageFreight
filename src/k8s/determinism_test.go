package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func svc(ns, name string, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.ServiceSpec{Selector: selector},
	}
}

// A merged family carries one label set per member. The dashboard Service selects the
// dashboard pod only; it must find the family through that member, not through
// whichever member's labels the family happened to inherit at merge time.
func TestServiceOwnerMatchesAnyMemberWorkload(t *testing.T) {
	family := AppKey{Namespace: "siem", Identity: "wazuh"}
	groups := map[AppKey]*appGroup{
		family: {
			identity:  WorkloadIdentity{Key: family, Source: "prefix"},
			podLabels: map[string]string{"app": "wazuh", "component": "indexer"},
			workloads: []workloadInfo{
				{name: "wazuh-indexer", labels: map[string]string{"app": "wazuh", "component": "indexer"}},
				{name: "wazuh-dashboard", labels: map[string]string{"app": "wazuh", "component": "dashboard"}},
			},
		},
	}
	owner, ok := serviceOwner(groups, svc("siem", "wazuh-dashboard", map[string]string{"app": "wazuh", "component": "dashboard"}))
	if !ok || owner != family {
		t.Fatalf("dashboard service: owner=%v ok=%v, want %v", owner, ok, family)
	}
	if _, ok := serviceOwner(groups, svc("siem", "other", map[string]string{"app": "nope"})); ok {
		t.Fatal("a selector matching no member must not attach")
	}
	if _, ok := serviceOwner(groups, svc("elsewhere", "wazuh-dashboard", map[string]string{"app": "wazuh", "component": "dashboard"})); ok {
		t.Fatal("namespace must match")
	}
}

// Two distinct groups can both satisfy a selector. Whichever wins, it must be the same
// one on every run — ownership feeds hostnames, exposure and the app/platform tier.
func TestServiceOwnerTieIsStable(t *testing.T) {
	a := AppKey{Namespace: "ns", Identity: "alpha"}
	b := AppKey{Namespace: "ns", Identity: "beta"}
	labels := map[string]string{"app": "shared"}
	mk := func() map[AppKey]*appGroup {
		return map[AppKey]*appGroup{
			a: {workloads: []workloadInfo{{name: "alpha", labels: labels}}},
			b: {workloads: []workloadInfo{{name: "beta", labels: labels}}},
		}
	}
	for i := 0; i < 50; i++ {
		owner, ok := serviceOwner(mk(), svc("ns", "shared", labels))
		if !ok || owner != a {
			t.Fatalf("run %d: owner=%v ok=%v, want %v every time", i, owner, ok, a)
		}
	}
}

// Equal display names are not equal records. Without a total order their relative
// position was the input order — and the input order was map iteration.
func TestSortRecordsTotalOrder(t *testing.T) {
	x := AppRecord{Key: AppKey{Namespace: "b-ns", Identity: "svc"}, FriendlyName: "Same", Category: "apps"}
	y := AppRecord{Key: AppKey{Namespace: "a-ns", Identity: "svc"}, FriendlyName: "same", Category: "apps"}
	for _, in := range [][]AppRecord{{x, y}, {y, x}} {
		recs := append([]AppRecord(nil), in...)
		sortRecords(recs)
		if recs[0].Key != y.Key || recs[1].Key != x.Key {
			t.Fatalf("got %v,%v want %v,%v", recs[0].Key, recs[1].Key, y.Key, x.Key)
		}
	}
}

// Retired entries with the same name in two namespaces must keep a fixed order too.
func TestGraveyardOrderTotal(t *testing.T) {
	m := &InventoryManifest{Apps: map[string]AppManifest{
		"z/app": {Lifecycle: AppLifecycle{State: "graveyard"}, Observed: AppObserved{Namespace: "z", Name: "app"}},
		"a/app": {Lifecycle: AppLifecycle{State: "graveyard"}, Observed: AppObserved{Namespace: "a", Name: "app"}},
	}}
	for i := 0; i < 20; i++ {
		g := GraveyardFromManifest(m)
		if len(g) != 2 || g[0].Namespace != "a" || g[1].Namespace != "z" {
			t.Fatalf("run %d: got %+v", i, g)
		}
	}
}
