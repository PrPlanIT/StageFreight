package prune

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEvictStaleKeepsRecentEntries(t *testing.T) {
	root := t.TempDir()
	stale, fresh := filepath.Join(root, "sf-governance-1234"), filepath.Join(root, "sf-mirror-5678")
	for _, d := range []string{stale, fresh} {
		if err := os.MkdirAll(filepath.Join(d, "x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	items, _, err := evictStale(root, tempMaxAge, false)
	if err != nil || len(items) != 1 {
		t.Fatalf("dry run: %v %v", items, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("a dry run removed an entry")
	}
	if _, _, err := evictStale(root, tempMaxAge, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale entry survives")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("recent entry removed")
	}
}
