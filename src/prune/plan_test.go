package prune

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

func fakeCacheRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"cache/go/build", "cache/go/downloads", "cache/go/mod", "cache/lint", "cache/substrate", "toolchains/go"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The SF cache root is an SF-owned NAMESPACE: every subsystem gets a lifecycle — named
// policies for the known ones, the backstop for everything else (present or future).
func TestPlan_NamespaceLifecycleCoversEverySubsystem(t *testing.T) {
	root := fakeCacheRoot(t)
	actions := Plan(nil, Options{CacheRoot: root}) // nil cfg: bare runner host

	byLabel := map[string]Action{}
	for _, a := range actions {
		if a.Class != ClassNamespace {
			t.Errorf("nil-config planning must yield ONLY namespace-class actions, got %s (%s)", a.Kind, a.Class)
		}
		byLabel[a.Label] = a
	}
	if a, ok := byLabel["cache/go/build"]; !ok || a.MaxSize == 0 {
		t.Error("cache/go/build must get the size-cap policy")
	}
	if a, ok := byLabel["cache/lint"]; !ok || a.Kind != KindLintCache {
		t.Error("cache/lint must get the per-entry LRU policy")
	}
	if a, ok := byLabel["cache units"]; !ok || a.Kind != KindUnits || a.MaxAge == 0 {
		t.Error("every other cache directory must get the unit lifecycle with the age backstop")
	}
	if _, ok := byLabel["disk pressure"]; ok {
		t.Error("no pressure action without a target while the disk is under the default watermark")
	}
	usedFraction = func(string) float64 { return 0.99 }
	full := Plan(nil, Options{CacheRoot: root})
	usedFraction = func(string) float64 { return 0.5 }
	if len(full) == 0 || full[len(full)-1].Kind != KindPressure || full[len(full)-1].Low != DefaultTarget-pressureHysteresis {
		t.Error("without a target, a disk past the default watermark must still get pressure relief")
	}
	if a := Plan(nil, Options{CacheRoot: root, Target: 0.000001}); len(a) == 0 || a[len(a)-1].Kind != KindPressure {
		t.Error("a pressure-gated plan must end with the pressure eviction")
	}
	if _, ok := byLabel["toolchains"]; !ok {
		t.Error("toolchains must get the retention lifecycle")
	}
}

// Property A, the authorization boundary: declared/host actions appear ONLY under
// HostCleanup — a config DECLARING adoptions is not enough without the authorization.
func TestPlan_DeclaredRequiresAuthorization(t *testing.T) {
	cfg := &config.Config{}
	cfg.BuildCache.Cleanup.Prune.Images.Refs = []config.RetentionRef{{Match: "electronuserland/builder:{t}", MaxAge: "14d"}}
	cfg.BuildCache.Cleanup.Prune.Images.Dangling.OlderThan = "72h"

	for _, a := range Plan(cfg, Options{CacheRoot: fakeCacheRoot(t)}) {
		if a.Class == ClassDeclared {
			t.Fatalf("declared-class action %q emitted WITHOUT host-cleanup authorization", a.Label)
		}
	}
	var declared int
	for _, a := range Plan(cfg, Options{CacheRoot: fakeCacheRoot(t), HostCleanup: true}) {
		if a.Class == ClassDeclared {
			declared++
		}
	}
	if declared != 2 { // refs + dangling/exited residue
		t.Fatalf("authorized planning must emit the declared adoptions, got %d", declared)
	}
}

// The provenance class: an image-stream action exists ONLY when the repo's own publish
// targets declare the stream — candidacy is the target's tag templates, policy its
// retention. No targets ⇒ no image action at all.
func TestPlan_ImageStreamFromOwnTargetsOnly(t *testing.T) {
	cfg := &config.Config{}
	cfg.Repos = append(cfg.Repos, config.RepoConfig{ID: "primary", Project: "PrPlanIT/StageFreight", Roles: []string{"primary"}})

	for _, a := range Plan(cfg, Options{CacheRoot: fakeCacheRoot(t)}) {
		if a.Kind == KindImageStream {
			t.Fatal("no publish targets ⇒ no image-stream action")
		}
	}

	cfg.Targets = []config.TargetConfig{{ID: "dev", Kind: "registry",
		Tags:      []string{"dev-{sha:8}", "latest-dev"},
		Retention: &config.RetentionPolicy{KeepLast: 6}}}
	var stream *Action
	for _, a := range Plan(cfg, Options{CacheRoot: fakeCacheRoot(t)}) {
		if a.Kind == KindImageStream {
			c := a
			stream = &c
		}
	}
	if stream == nil {
		t.Fatal("declared registry target with retention must yield the local image-stream action")
	}
	if stream.Class != ClassProvenance || stream.Policy.KeepLast != 6 || len(stream.Templates) != 2 {
		t.Fatalf("stream must carry the target's own templates+policy, got %+v", stream)
	}
	if stream.Streams[0] != "stagefreight" {
		t.Fatalf("stream slug must derive from the primary project, got %v", stream.Streams)
	}
}

// The pressure gate: a healthy disk short-circuits to zero actions.
func TestPlan_PressureGateHealthyNoop(t *testing.T) {
	root := fakeCacheRoot(t)
	if used := UsedFraction(root); used > 0.99 {
		t.Skip("test filesystem effectively full; gate indistinguishable")
	}
	if actions := Plan(nil, Options{CacheRoot: root, Target: 0.999999}); len(actions) != 0 {
		t.Fatalf("under target must be a no-op, got %d actions", len(actions))
	}
}

// makeUnit creates a unit dir leased at lastUse ago, holding one file of size bytes.
func makeUnit(t *testing.T, dir string, size int, lastUse time.Duration) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	lease := filepath.Join(dir, paths.UnitLeaseName)
	if err := os.WriteFile(lease, nil, 0o664); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Add(-lastUse)
	if err := os.Chtimes(lease, mt, mt); err != nil {
		t.Fatal(err)
	}
}

// holdLease holds dir's lease shared from a separate open file, as another run would.
func holdLease(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, paths.UnitLeaseName))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
}

// Units are evicted whole by their recorded last use; one in use survives however old,
// and dry-run only lists.
func TestEvictUnits_ByRecordedUse(t *testing.T) {
	cache := t.TempDir()
	makeUnit(t, filepath.Join(cache, "trivy"), 100, 40*24*time.Hour)
	makeUnit(t, filepath.Join(cache, "go", "downloads"), 100, 40*24*time.Hour)
	makeUnit(t, filepath.Join(cache, "zig"), 100, time.Hour)
	makeUnit(t, filepath.Join(cache, "grype"), 100, 40*24*time.Hour)
	holdLease(t, filepath.Join(cache, "grype"))
	a := Action{Kind: KindUnits, Path: cache, MaxAge: backstopMaxAge}

	items, _, err := evictUnits(a, false, false)
	if err != nil || len(items) != 3 {
		t.Fatalf("dry-run: %v %v, want the three old units listed", items, err)
	}
	if _, err := os.Stat(filepath.Join(cache, "trivy")); err != nil {
		t.Fatal("dry-run must not delete")
	}

	if _, _, err := evictUnits(a, false, true); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{"trivy": false, "go/downloads": false, "zig": true, "grype": true} {
		_, err := os.Stat(filepath.Join(cache, rel))
		if (err == nil) != want {
			t.Errorf("%s present=%v, want %v", rel, err == nil, want)
		}
	}
}

// A directory with no use record is evicted only while this run holds the cache root
// exclusively, and a unit's own contents are never evicted piecemeal.
func TestEvictUnits_UnrecordedNeedsExclusiveRoot(t *testing.T) {
	cache := t.TempDir()
	makeUnit(t, filepath.Join(cache, "go", "downloads"), 100, time.Hour)
	inside := filepath.Join(cache, "go", "downloads", "github.com")
	foreign := filepath.Join(cache, "go", "mod")
	for _, d := range []string{inside, foreign} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-40 * 24 * time.Hour)
		_ = os.Chtimes(d, old, old)
	}
	a := Action{Kind: KindUnits, Path: cache, MaxAge: backstopMaxAge}

	if _, _, err := evictUnits(a, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("an unrecorded directory must survive while other runs may be using it")
	}
	if _, _, err := evictUnits(a, true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err == nil {
		t.Error("an unrecorded directory past the backstop must be evicted with the root held exclusively")
	}
	if _, err := os.Stat(inside); err != nil {
		t.Error("a unit's contents must never be evicted on their own")
	}
}

// Under pressure, units are evicted least recently used first until enough is freed,
// skipping units in use.
func TestEvictUnderPressure_LRU(t *testing.T) {
	cache := t.TempDir()
	makeUnit(t, filepath.Join(cache, "oldest"), 1000, 10*time.Hour)
	makeUnit(t, filepath.Join(cache, "older"), 1000, 5*time.Hour)
	makeUnit(t, filepath.Join(cache, "busy"), 1000, 20*time.Hour)
	makeUnit(t, filepath.Join(cache, "recent"), 1000, time.Hour)
	holdLease(t, filepath.Join(cache, "busy"))
	old := bytesOver
	t.Cleanup(func() { bytesOver = old })
	bytesOver = func(string, float64) int64 { return 1500 }

	if _, _, err := evictUnderPressure(Action{Kind: KindPressure, Path: cache, Low: 0.75}, true); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{"oldest": false, "older": false, "busy": true, "recent": true} {
		_, err := os.Stat(filepath.Join(cache, rel))
		if (err == nil) != want {
			t.Errorf("%s present=%v, want %v", rel, err == nil, want)
		}
	}
}

// An eviction interrupted after renaming its unit aside is finished by the next pass.
func TestEvictUnits_FinishesInterruptedEviction(t *testing.T) {
	cache := t.TempDir()
	tomb := filepath.Join(cache, ".zig.evicted-123")
	makeUnit(t, tomb, 100, time.Minute)
	if _, _, err := evictUnits(Action{Kind: KindUnits, Path: cache, MaxAge: backstopMaxAge}, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tomb); err == nil {
		t.Fatal("a tombstone must be removed")
	}
}

// Go's module cache is read-only (0555 dirs); its unit is evicted whole all the same.
func TestEvictUnits_ReadOnlyModuleCache(t *testing.T) {
	cache := t.TempDir()
	unitDir := filepath.Join(cache, "go", "downloads")
	makeUnit(t, unitDir, 10, 40*24*time.Hour)
	pkg := filepath.Join(unitDir, "example.com", "lib@v1.0.0")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "lib.go"), []byte("package lib"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{pkg, filepath.Dir(pkg)} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = paths.RemoveAllWritable(cache) })

	if _, _, err := evictUnits(Action{Kind: KindUnits, Path: cache, MaxAge: backstopMaxAge}, false, true); err != nil {
		t.Fatalf("evicting a read-only module cache: %v", err)
	}
	if _, err := os.Stat(unitDir); err == nil {
		t.Fatal("the read-only module cache must be removed whole")
	}
}

// The Go build cache is trimmed per entry, least recently used first: a hot entry beside
// cold ones survives.
func TestEvictGoBuildCache_PerEntryLRU(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, size int, age time.Duration) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	write("5a/hot-a", 1000, time.Minute)
	write("5a/cold-a", 1000, 48*time.Hour)
	write("fd/warm-d", 1000, 2*time.Hour)
	write("README", 10, 72*time.Hour)
	write("trim.txt", 10, 72*time.Hour)

	if _, _, err := evictFilesLRU(dir, goBuildEntry, 0, 2000, 0, true); err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"5a/hot-a", "fd/warm-d", "README", "trim.txt"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s must survive: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "5a/cold-a")); err == nil {
		t.Error("the least recently used entry must be evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "5a")); err != nil {
		t.Error("the prefix dir holding a hot entry must survive")
	}
}

// The Go build cache's age floor: under it, nothing is evicted even over the size cap.
func TestEvictGoBuildCache_MinAge(t *testing.T) {
	dir := t.TempDir()
	for name, age := range map[string]time.Duration{"aa/cold-a": 48 * time.Hour, "bb/warm-a": time.Hour} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, 1000), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		_ = os.Chtimes(p, mt, mt)
	}
	if _, _, err := evictFilesLRU(dir, goBuildEntry, 0, 1, 24*time.Hour, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "aa/cold-a")); err == nil {
		t.Error("the cold entry must be evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "bb/warm-a")); err != nil {
		t.Error("an entry under the age floor must survive even over the size cap")
	}
}

// Host residue is pruned with a Docker daemon and skipped cleanly without one.
func TestPruneHostResidue_DockerAndDaemonlessRunners(t *testing.T) {
	old := dockerDaemon
	t.Cleanup(func() { dockerDaemon = old })
	a := Action{Kind: KindHostResidue, Until: "7d"}

	dockerDaemon = func() (string, bool) { return "/var/run/docker.sock", true }
	items, skipped, err := pruneHostResidue(context.Background(), a, false)
	if err != nil || skipped != "" || len(items) != 2 {
		t.Fatalf("docker runner: items=%v skipped=%q err=%v, want both prunes planned", items, skipped, err)
	}

	dockerDaemon = func() (string, bool) { return "", false }
	items, skipped, err = pruneHostResidue(context.Background(), a, true)
	if err != nil || skipped == "" || len(items) != 0 {
		t.Fatalf("daemonless runner: items=%v skipped=%q err=%v, want a clean skip", items, skipped, err)
	}
}

// The cache root is released once its last action is done: a daemon-side prune that
// follows runs with the root free for other runs.
func TestExecute_ReleasesCacheRootBeforeDaemonPrunes(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cache", "lint"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := dockerDaemon
	t.Cleanup(func() { dockerDaemon = old })
	freeDuringDaemonPrune := false
	dockerDaemon = func() (string, bool) {
		probe := exec.Command("flock", "-x", "-n", filepath.Join(root, paths.CacheLockName), "true")
		freeDuringDaemonPrune = probe.Run() == nil
		return "", false
	}
	actions := []Action{
		{Kind: KindUnits, Class: ClassNamespace, Label: "cache units", Path: filepath.Join(root, "cache"), MaxAge: time.Hour, LockRoot: root},
		{Kind: KindHostResidue, Class: ClassDeclared, Label: "host residue", Until: "7d"},
	}
	Execute(context.Background(), actions, true)
	if !freeDuringDaemonPrune {
		t.Fatal("the cache root must be released before the daemon-side prunes run")
	}
}

// The lint cache is evicted entry by entry by last use, across every project's results,
// to its size cap — busy or not, since removing an entry costs a run only a cache miss.
func TestExecute_LintCachePerEntryLRU(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "cache", "lint")
	write := func(rel string, size int, age time.Duration) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		_ = os.Chtimes(p, mt, mt)
	}
	write("projA/aa/hot.json", 1000, time.Minute)
	write("projA/aa/stale.json", 1000, 10*24*time.Hour)
	write("projB/bb/old.json", 1000, 3*time.Hour)
	write("projB/bb/new.json", 1000, 2*time.Minute)
	// The project dir is old, the entries in it are not: eviction must go by entry.
	old := time.Now().Add(-30 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "projA"), old, old)

	res := Execute(context.Background(), []Action{{Kind: KindLintCache, Class: ClassNamespace,
		Label: "cache/lint", Path: dir, MaxAge: 7 * 24 * time.Hour, MaxSize: 2000}}, true)
	if len(res) != 1 || res[0].Err != nil {
		t.Fatalf("result: %+v", res)
	}
	for rel, want := range map[string]bool{"projA/aa/hot.json": true, "projA/aa/stale.json": false, "projB/bb/old.json": false, "projB/bb/new.json": true} {
		_, err := os.Stat(filepath.Join(dir, rel))
		if (err == nil) != want {
			t.Errorf("%s present=%v, want %v", rel, err == nil, want)
		}
	}
}

// Toolchain retention evicts versions beyond policy even while the cache is busy, but
// never one a running job holds.
func TestExecute_ToolchainRetentionSkipsLeasedVersion(t *testing.T) {
	root := t.TempDir()
	tc := filepath.Join(root, "toolchains")
	for i, v := range []string{"1.0.0", "1.1.0", "1.2.0", "1.3.0"} {
		dir := filepath.Join(tc, "go", v)
		makeUnit(t, dir, 10, time.Duration(10-i)*time.Hour)
		meta := `{"tool":"go","version":"` + v + `","installed_at":"` + time.Now().Add(-time.Duration(10-i)*time.Hour).Format(time.RFC3339) + `"}`
		if err := os.WriteFile(filepath.Join(dir, ".metadata.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	holdLease(t, filepath.Join(tc, "go", "1.0.0"))

	res := Execute(context.Background(), []Action{{Kind: KindToolchains, Class: ClassNamespace, Label: "toolchains", Path: tc}}, true)
	if len(res) != 1 || res[0].Err != nil || res[0].Skipped != "" {
		t.Fatalf("result: %+v", res)
	}
	for v, want := range map[string]bool{"1.0.0": true, "1.1.0": false, "1.2.0": true, "1.3.0": true} {
		_, err := os.Stat(filepath.Join(tc, "go", v))
		if (err == nil) != want {
			t.Errorf("go/%s present=%v, want %v", v, err == nil, want)
		}
	}
}

// TestMain runs every test against a half-full disk unless it sets its own, so no test
// depends on the state of the machine it runs on.
func TestMain(m *testing.M) {
	usedFraction = func(string) float64 { return 0.5 }
	os.Exit(m.Run())
}
