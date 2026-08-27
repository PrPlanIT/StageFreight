package prune

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
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
