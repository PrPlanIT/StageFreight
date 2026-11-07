package prune

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// fakeDocker puts a docker on PATH that records its arguments and exits with rc.
func fakeDocker(t *testing.T, rc string) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + logPath + "\nexit " + rc + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return logPath
}

func TestPruneBuilderReachesStandaloneBuildkitd(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	certs := t.TempDir()
	for _, f := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if err := os.WriteFile(filepath.Join(certs, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BUILDKIT_HOST", "tcp://"+ln.Addr().String())
	t.Setenv("BUILDKIT_CERT_PATH", certs)
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	logPath := fakeDocker(t, "0")

	items, skipped, err := pruneBuilder(context.Background(), Action{
		Builder: "sf-builder", KeepStore: "8GB", Until: "7d", MinFree: 15 << 30,
	}, true)
	if err != nil || skipped != "" || len(items) != 1 {
		t.Fatalf("items=%v skipped=%q err=%v", items, skipped, err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(raw)
	for _, want := range []string{
		"buildx inspect --bootstrap sf-builder",
		"--filter until=168h",
		"buildx prune --builder sf-builder --force --min-free-space 16106127360",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q in:\n%s", want, calls)
		}
	}
}

func TestPruneBuilderAbsentIsNotAFailure(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "tcp://127.0.0.1:1")
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'ERROR: no builder \"sf-builder\" found' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	_, skipped, err := pruneBuilder(context.Background(), Action{Builder: "sf-builder"}, true)
	if err != nil || skipped != "no sf-builder on this host" {
		t.Fatalf("skipped=%q err=%v", skipped, err)
	}
}

func TestPlanAsksBuilderForFreeSpaceOnlyUnderPressure(t *testing.T) {
	root := t.TempDir()
	origUsed, origTotal := usedFraction, diskTotal
	t.Cleanup(func() { usedFraction, diskTotal = origUsed, origTotal })
	diskTotal = func(string) int64 { return 100 << 30 }

	cfg := &config.Config{}
	cfg.BuildCache.Mode = "hybrid"
	minFree := func() int64 {
		for _, a := range Plan(cfg, Options{CacheRoot: root}) {
			if a.Kind == KindBuildkit {
				return a.MinFree
			}
		}
		t.Fatal("no buildkit action planned")
		return 0
	}

	usedFraction = func(string) float64 { return 0.50 }
	if got := minFree(); got != 0 {
		t.Errorf("healthy disk: MinFree = %d, want 0", got)
	}
	usedFraction = func(string) float64 { return 0.92 }
	if got, want := minFree(), int64(25<<30); got != want {
		t.Errorf("over target: MinFree = %d, want %d", got, want)
	}
}

// The size cap holds against what entries occupy: many tiny files fill a disk while
// their combined length stays under it.
func TestEvictionCountsSpaceOnDisk(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 100; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d.json", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if dirSize(dir) <= 100 {
		t.Skip("filesystem allocates no block for a one-byte file")
	}
	items, _, err := evictFilesLRU(dir, lintEntry, 0, 1000, 0, true)
	if err != nil || len(items) == 0 {
		t.Fatalf("items=%d err=%v: 100 one-byte files occupy more than the 1000-byte cap", len(items), err)
	}
}
