package docker

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// writeGeneration writes a cache generation of size bytes at dir.
func writeGeneration(t *testing.T, dir string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", "b"), make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A build imports the current generation and exports a new one; promotion makes the
// export current and drops the old generation and abandoned exports.
func TestLocalCache_GenerationSwap(t *testing.T) {
	t.Setenv(paths.CacheRootEnv, t.TempDir())
	cfg := config.LocalCacheConfig{}
	from, to := localFlags("repo", cfg)
	if len(from) != 0 || len(to) != 1 {
		t.Fatalf("cold cache: from=%v to=%v, want export only", from, to)
	}
	dir := LocalCacheDir("repo", cfg)
	writeGeneration(t, filepath.Join(dir, localCurrent), 10)
	writeGeneration(t, filepath.Join(dir, localNextPrefix+"999999"), 10)
	from, to = localFlags("repo", cfg)
	if len(from) != 1 || from[0].Ref != filepath.Join(dir, localCurrent) || to[0].Ref != localNext(dir) {
		t.Fatalf("warm cache: from=%v to=%v", from, to)
	}

	writeGeneration(t, localNext(dir), 20)
	res := promoteLocalCache(dir, config.LocalRetention{})
	if !res.Promoted || res.Bytes < 20 {
		t.Fatalf("promote: %+v", res)
	}
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 || names[0] != localCurrent {
		t.Fatalf("after promote: %v, want only the current generation", names)
	}
}

// While another run of the repo uses its cache, the current generation is kept and this
// build's export discarded; an export over max_size is discarded too.
func TestLocalCache_PromoteKeepsInUseAndBounds(t *testing.T) {
	t.Setenv(paths.CacheRootEnv, t.TempDir())
	cfg := config.LocalCacheConfig{}
	localFlags("repo", cfg)
	dir := LocalCacheDir("repo", cfg)
	writeGeneration(t, filepath.Join(dir, localCurrent), 10)

	writeGeneration(t, localNext(dir), 2000)
	if res := promoteLocalCache(dir, config.LocalRetention{MaxSize: "1KB"}); res.Promoted {
		t.Fatalf("an export over max_size must not be promoted: %+v", res)
	}
	if _, err := os.Stat(localNext(dir)); err == nil {
		t.Error("a discarded export must be removed")
	}

	f, err := os.Open(filepath.Join(dir, paths.UnitLeaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	writeGeneration(t, localNext(dir), 10)
	if res := promoteLocalCache(dir, config.LocalRetention{}); res.Promoted {
		t.Fatalf("must not promote while another run uses the cache: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, localCurrent, "index.json")); err != nil {
		t.Error("the generation another run imports must be kept")
	}
}
