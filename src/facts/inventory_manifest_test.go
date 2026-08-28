package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/k8s"
)

func writeManifest(t *testing.T, root, cluster string, complete bool, states ...string) {
	t.Helper()
	dir := filepath.Join(root, ".stagefreight", "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := k8s.InventoryManifest{
		SchemaVersion:   1,
		Cluster:         cluster,
		GeneratedAt:     time.Unix(0, 0).UTC(),
		DiscoveryStatus: k8s.DiscoveryStatus{Complete: complete, Source: "live_cluster"},
		Apps:            map[string]k8s.AppManifest{},
	}
	for i, s := range states {
		m.Apps[cluster+"/app"+string(rune('a'+i))] = k8s.AppManifest{
			Lifecycle: k8s.AppLifecycle{State: s},
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "k8s-inventory-"+cluster+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Graveyard entries are apps the cluster no longer runs, kept so their disappearance is
// auditable. Counting them would advertise an estate larger than the one that exists.
//
// "missing" DOES count: with graveyarding gated on sustained absence it means absent
// from the last sweep but still believed to exist. Excluding it would make the badge
// track which workloads answered during one pass, so a restart or drain would move it.
func TestManifestAppCountIncludesMissingNotGraveyard(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "dungeon", true, "active", "missing", "graveyard", "active")
	got, ok := manifestAppCount(root, "dungeon")
	if !ok {
		t.Fatal("a complete manifest must resolve")
	}
	if got != 3 {
		t.Errorf("got %d, want 3 (2 active + 1 missing; graveyard excluded)", got)
	}
}

// A partial sweep undercounts, and a number that is quietly wrong is worse than the
// "n/a" the caller falls back to.
func TestManifestAppCountRejectsIncompleteDiscovery(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, "dungeon", false, "active", "active")
	if _, ok := manifestAppCount(root, "dungeon"); ok {
		t.Error("an incomplete discovery must not produce a count")
	}
}

// No manifest is the fallback case — the caller then tries live discovery.
func TestManifestAppCountAbsent(t *testing.T) {
	if _, ok := manifestAppCount(t.TempDir(), "dungeon"); ok {
		t.Error("a missing manifest must report false, not zero")
	}
}
