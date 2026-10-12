package k8s

import (
	"slices"
	"testing"
	"time"
)

func missingFor(d time.Duration, now time.Time) *InventoryManifest {
	since := now.Add(-d)
	return &InventoryManifest{
		Apps: map[string]AppManifest{
			"ns/app": {Lifecycle: AppLifecycle{State: "missing", MissingSince: &since}},
		},
	}
}

// A workload absent from one sweep must NOT be retired. Restarts, drains, image pulls
// and failovers all finish well inside the window, and burying them there is the churn
// this guards against.
func TestGraveyardWaitsForSustainedAbsence(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	m := missingFor(GraveyardAfter-time.Minute, now)
	ReconcileLifecycle(m, nil, true, now)
	if got := m.Apps["ns/app"].Lifecycle.State; got != "missing" {
		t.Errorf("just inside the window: got %q, want missing", got)
	}
}

// Sustained absence still retires — the buffer delays the decision, it does not remove it.
func TestGraveyardAfterThreshold(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	m := missingFor(GraveyardAfter+time.Minute, now)
	ReconcileLifecycle(m, nil, true, now)
	if got := m.Apps["ns/app"].Lifecycle.State; got != "graveyard" {
		t.Errorf("past the window: got %q, want graveyard", got)
	}
}

// A missing entry with no MissingSince (written before this field was populated) must
// not be trapped in missing forever.
func TestGraveyardWithoutMissingSince(t *testing.T) {
	now := time.Now().UTC()
	m := &InventoryManifest{Apps: map[string]AppManifest{
		"ns/app": {Lifecycle: AppLifecycle{State: "missing"}},
	}}
	ReconcileLifecycle(m, nil, true, now)
	if got := m.Apps["ns/app"].Lifecycle.State; got != "graveyard" {
		t.Errorf("no MissingSince: got %q, want graveyard", got)
	}
}

// Revival stays immediate and clears the clock: if it is back, it is back.
func TestSightingRevivesAndResetsClock(t *testing.T) {
	now := time.Now().UTC()
	m := missingFor(GraveyardAfter/2, now)
	ReconcileLifecycle(m, []AppRecord{{Key: AppKey{Namespace: "ns", Identity: "app"}}}, true, now)
	got := m.Apps["ns/app"].Lifecycle
	if got.State != "active" {
		t.Errorf("got %q, want active", got.State)
	}
	if got.MissingSince != nil {
		t.Error("MissingSince must be cleared on revival, or the next absence inherits a stale clock")
	}
}

// An entry whose workload now runs as a component of another live app was superseded by
// an identity change, not retired. It must leave the manifest instead of sitting in the
// graveyard forever under a key that will never come back.
func TestSupersededEntryIsDroppedNotRetired(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	entry := func(state string, images ...string) AppManifest {
		return AppManifest{Lifecycle: AppLifecycle{State: state}, Observed: AppObserved{Images: images}}
	}
	m := &InventoryManifest{Apps: map[string]AppManifest{
		// Renamed, regrouped or moved: the same software runs under another key.
		"ns/roundcube":        entry("graveyard", "docker.io/roundcube/roundcubemail:1.6", "docker.io/library/mysql:8"),
		"old-ns/kyverno":      entry("missing", "ghcr.io/kyverno/kyverno:v1.19.0"),
		"ns/gitlab-sentinel":  entry("graveyard", "quay.io/opstree/redis-sentinel:v7"),
		"ns/dolibarr-mariadb": entry("graveyard", "docker.io/linuxserver/mariadb:11"),
		"old-ns/edge-gateway": entry("graveyard", "registry.istio.io/release/proxyv2:1.30"),
		// Retired: its own software runs nowhere, though its database image does.
		"ns/dolibarr": entry("graveyard", "docker.io/dolibarr/dolibarr:23", "docker.io/linuxserver/mariadb:11"),
	}}
	live := []AppRecord{{
		Key:    AppKey{Namespace: "ns", Identity: "roundcube-primary-gmail"},
		Images: []ImageRef{{Repository: "roundcube/roundcubemail", Tag: "1.6.9"}, {Repository: "mysql", Tag: "8"}},
	}, {
		Key:    AppKey{Namespace: "new-ns", Identity: "kyverno"},
		Images: []ImageRef{{Repository: "ghcr.io/kyverno/kyverno", Tag: "v1.20.0"}},
	}, {
		Key:    AppKey{Namespace: "ns", Identity: "gitlab"},
		Images: []ImageRef{{Repository: "gitlab/gitlab-ce", Tag: "18"}, {Repository: "quay.io/opstree/redis-sentinel", Tag: "v7"}},
	}, {
		Key:    AppKey{Namespace: "edge", Identity: "edge-gateway"},
		Images: []ImageRef{{Repository: "docker.io/istio/proxyv2", Tag: "1.31.1"}},
	}, {
		Key:    AppKey{Namespace: "ns", Identity: "osticket"},
		Images: []ImageRef{{Repository: "osticket/osticket", Tag: "1"}, {Repository: "docker.io/linuxserver/mariadb", Tag: "11"}},
	}}
	ReconcileLifecycle(m, live, true, now)
	for _, k := range []string{"ns/roundcube", "old-ns/kyverno", "ns/gitlab-sentinel", "ns/dolibarr-mariadb", "old-ns/edge-gateway"} {
		if _, ok := m.Apps[k]; ok {
			t.Errorf("%s still runs under another key: must be dropped, got %q", k, m.Apps[k].Lifecycle.State)
		}
	}
	if got := m.Apps["ns/dolibarr"].Lifecycle.State; got != "graveyard" {
		t.Errorf("an app whose own software is gone stays retired: got %q", got)
	}
}

func TestProductsIgnoreRegistryTagAndBackingServices(t *testing.T) {
	got := Products([]string{"quay.io/ceph/ceph:v19", "docker.io/library/redis:8", "ceph/ceph:v20"})
	if !slices.Equal(got, []string{"ceph/ceph"}) {
		t.Errorf("got %v", got)
	}
	if got := Products([]string{"docker.io/library/redis:8"}); !slices.Equal(got, []string{"redis"}) {
		t.Errorf("an app made only of a backing service is that service: got %v", got)
	}
}

func TestFoldCarriesSubordinateImages(t *testing.T) {
	got := FoldSubordinates([]AppRecord{{
		Key:        AppKey{Namespace: "ns", Identity: "gitlab"},
		Components: []ComponentRef{{Name: "gitlab-webservice", Role: RolePrimary}},
		Images:     []ImageRef{{Repository: "gitlab/gitlab-ce"}},
	}, {
		Key:        AppKey{Namespace: "ns", Identity: "gitlab-sentinel"},
		Components: []ComponentRef{{Name: "gitlab-sentinel-sentinel", Role: RoleSentinel}},
		Images:     []ImageRef{{Repository: "quay.io/opstree/redis-sentinel"}},
	}})
	if len(got) != 1 || len(got[0].Images) != 2 {
		t.Fatalf("the folded sentinel's image belongs to gitlab: got %+v", got)
	}
}

func TestGraveyardListsEachRetiredProductOnce(t *testing.T) {
	march := time.Date(2026, 3, 30, 0, 0, 0, 0, time.UTC)
	oct := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	gone := func(ns, name string, since time.Time, state string, images ...string) AppManifest {
		return AppManifest{
			Lifecycle: AppLifecycle{State: state, MissingSince: &since},
			Observed:  AppObserved{Namespace: ns, Name: name, Images: images},
		}
	}
	m := &InventoryManifest{Apps: map[string]AppManifest{
		"ns/erpnext-frontend": gone("ns", "erpnext-frontend", march, "graveyard", "frappe/erpnext:v15"),
		"ns/erpnext":          gone("ns", "erpnext", oct, "graveyard", "frappe/erpnext:v15", "mariadb:10.11"),
		"ns/erpnext-sites":    gone("ns", "erpnext-sites", march, "graveyard", "frappe/erpnext:v15"),
		"ns/restarting":       gone("ns", "restarting", oct, "missing", "example/restarting:1"),
	}}
	got := GraveyardFromManifest(m)
	want := []GraveyardEntry{{Name: "erpnext", Namespace: "ns", Reason: "missing since 2026-10-12"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}
