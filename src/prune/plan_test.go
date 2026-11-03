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
	for _, d := range []string{"cache/go/build", "cache/go/downloads", "cache/lint", "cache/substrate", "toolchains/go"} {
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
	if a, ok := byLabel["cache/substrate"]; !ok || a.MaxAge == 0 {
		t.Error("an unnamed subsystem must get the age backstop — the open set stays bounded")
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

// evictDir: age evicts old entries, then size evicts oldest-first to the cap; dry-run
// enumerates without deleting.
func TestEvictDir_AgeThenSize(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, size int, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	write("ancient", 100, 40*24*time.Hour)
	write("old", 3000, 10*24*time.Hour)
	write("fresh", 3000, time.Hour)

	// Dry-run: plan only.
	items, _, err := evictDir(dir, 30*24*time.Hour, 4000, false)
	if err != nil || len(items) != 2 { // ancient (age) + old (size, oldest-first)
		t.Fatalf("dry-run: want 2 evictions, got %v (%v)", items, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ancient")); statErr != nil {
		t.Fatal("dry-run must not delete")
	}

	// Confirm: deletes; fresh survives.
	if _, _, err := evictDir(dir, 30*24*time.Hour, 4000, true); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "fresh")); statErr != nil {
		t.Fatal("fresh entry must survive")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ancient")); statErr == nil {
		t.Fatal("aged entry must be deleted on confirm")
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

	if _, _, err := evictGoBuildCache(dir, 2000, 0, true); err != nil {
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

// Go's module cache is read-only (0555 dirs); eviction removes a module whole
// instead of failing halfway and leaving a module Go trusts as present.
func TestEvictDir_ReadOnlyModuleCache(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "example.com")
	pkg := filepath.Join(mod, "lib@v1.0.0")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "lib.go"), []byte("package lib"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{pkg, mod} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(mod, old, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeAllWritable(mod) })

	if _, _, err := evictDir(dir, 30*24*time.Hour, 0, true); err != nil {
		t.Fatalf("evicting a read-only module tree: %v", err)
	}
	if _, err := os.Stat(mod); err == nil {
		t.Fatal("the read-only module tree must be removed whole")
	}
}

// While another run holds the cache, eviction still reclaims entries unused for 24h and
// leaves recent ones, and says so.
func TestExecute_BusyCacheEvictsOnlyColdEntries(t *testing.T) {
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "cache", "lint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, age := range map[string]time.Duration{"cold": 40 * 24 * time.Hour, "warm": 2 * time.Hour} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		_ = os.Chtimes(p, mt, mt)
	}

	lock := filepath.Join(root, paths.CacheLockName)
	holder := exec.Command("flock", "-s", lock, "sleep", "30")
	holder.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // flock's child inherits the lock
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-holder.Process.Pid, syscall.SIGKILL); _ = holder.Wait() }()
	for i := 0; i < 200 && exec.Command("flock", "-x", "-n", lock, "true").Run() == nil; i++ {
	}

	actions := []Action{{Kind: KindEvictDir, Class: ClassNamespace, Label: "cache/lint",
		Path: dir, MaxAge: time.Hour, LockRoot: root}}
	res := Execute(context.Background(), actions, true)
	if len(res) != 1 || res[0].Note == "" {
		t.Fatalf("a busy-cache eviction must say it was narrowed, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "cold")); err == nil {
		t.Error("an entry unused for 40 days must be evicted even while the cache is in use")
	}
	if _, err := os.Stat(filepath.Join(dir, "warm")); err != nil {
		t.Error("an entry used 2h ago must survive while the cache is in use")
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
	if _, _, err := evictGoBuildCache(dir, 1, 24*time.Hour, true); err != nil {
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
		{Kind: KindEvictDir, Class: ClassNamespace, Label: "cache/lint", Path: filepath.Join(root, "cache", "lint"), MaxAge: time.Hour, LockRoot: root},
		{Kind: KindHostResidue, Class: ClassDeclared, Label: "host residue", Until: "7d"},
	}
	Execute(context.Background(), actions, true)
	if !freeDuringDaemonPrune {
		t.Fatal("the cache root must be released before the daemon-side prunes run")
	}
}
