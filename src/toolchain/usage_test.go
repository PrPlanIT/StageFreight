package toolchain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

var base = time.Now().Add(-100 * 24 * time.Hour)

// install fakes a cached install of tool@ver installed at day d (days after base), with
// optional last-used and pinned-use marker days (negative = absent).
func install(t *testing.T, root, tool, ver string, d, used, pinned int) {
	t.Helper()
	dir := filepath.Join(root, tool, ver)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(Metadata{Tool: tool, Version: ver, InstalledAt: day(d).UTC().Format(time.RFC3339)})
	if err := os.WriteFile(filepath.Join(dir, metadataFile), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := func(name string, d int) {
		if d < 0 {
			return
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, day(d), day(d)); err != nil {
			t.Fatal(err)
		}
	}
	stamp(lastUsedFile, used)
	stamp(pinnedUsedFile, pinned)
}

func day(d int) time.Time { return base.Add(time.Duration(d) * 24 * time.Hour) }

func pruned(t *testing.T, root string, policy config.RetentionPolicy, pins map[string]string) []string {
	t.Helper()
	cands, err := PlanVersionRetention(root, policy, pins)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cands {
		out = append(out, c.Version)
	}
	sort.Strings(out)
	return out
}

// TestRetention_RanksByLastUse: an old install another repo still uses outlives newer
// installs nobody has touched since.
func TestRetention_RanksByLastUse(t *testing.T) {
	root := t.TempDir()
	install(t, root, "trivy", "0.70.0", 1, 90, -1) // old, but used yesterday
	install(t, root, "trivy", "0.74.0", 50, -1, -1)
	install(t, root, "trivy", "0.75.0", 60, -1, -1)
	install(t, root, "trivy", "0.76.1", 70, 95, -1)
	got := pruned(t, root, config.RetentionPolicy{KeepLast: 2}, nil)
	if len(got) != 2 || got[0] != "0.74.0" || got[1] != "0.75.0" {
		t.Errorf("pruned %v, want [0.74.0 0.75.0] (0.76.1 and 0.70.0 are the two most recently used)", got)
	}
}

// TestRetention_KeepsPinnedBesideNewest: a version some repo pins survives even when
// more recently used versions fill keep_last, and the newest stays too.
func TestRetention_KeepsPinnedBesideNewest(t *testing.T) {
	root := t.TempDir()
	install(t, root, "kubectl", "1.31.0", 1, 60, 60) // pinned by a repo that ran 40 days ago
	install(t, root, "kubectl", "1.36.0", 70, 80, -1)
	install(t, root, "kubectl", "1.37.0", 75, 90, -1)
	install(t, root, "kubectl", "1.37.1", 80, 99, -1)
	got := pruned(t, root, config.RetentionPolicy{KeepLast: 2}, nil)
	if len(got) != 1 || got[0] != "1.36.0" {
		t.Errorf("pruned %v, want only 1.36.0 (1.31.0 pinned, 1.37.x most recently used)", got)
	}

	// The pinned pass is bounded by the same policy: a newer pin displaces an older one
	// past keep_last, and max_age alone ages pins out.
	install(t, root, "kubectl", "1.34.2", 72, 85, 85)
	got = pruned(t, root, config.RetentionPolicy{KeepLast: 1}, nil)
	if contains(got, "1.37.1") || contains(got, "1.34.2") || !contains(got, "1.31.0") {
		t.Errorf("keep_last 1 keeps the newest use and the newest pin; pruned %v", got)
	}
	got = pruned(t, root, config.RetentionPolicy{MaxAge: "30d"}, nil)
	if !contains(got, "1.31.0") || contains(got, "1.34.2") {
		t.Errorf("max_age 30d ages out the pin last used 40 days ago only; pruned %v", got)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestMarkUsed: a resolved install gets its last-used marker, and its pinned marker only
// for an exact selection.
func TestMarkUsed(t *testing.T) {
	cache := t.TempDir()
	t.Setenv(paths.CacheRootEnv, cache)
	root := InstallRoot(t.TempDir())
	install(t, root, "cosign", "3.0.6", 1, -1, -1)
	r := Result{Tool: "cosign", Version: "3.0.6", Path: CacheBinPathIn(root, "cosign", "3.0.6", "cosign")}

	markUsed("", r, false)
	dir := CacheDirIn(root, "cosign", "3.0.6")
	if usedAt(dir, lastUsedFile).IsZero() || !usedAt(dir, pinnedUsedFile).IsZero() {
		t.Fatal("a tracked use must stamp last-used only")
	}
	markUsed("", r, true)
	if usedAt(dir, pinnedUsedFile).IsZero() {
		t.Fatal("an exact selection must stamp pinned use")
	}
}
