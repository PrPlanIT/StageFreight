package disk

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirSize(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 100)
	writeFile(t, filepath.Join(root, "sub", "b"), 250)
	writeFile(t, filepath.Join(root, "sub", "deep", "c"), 400)
	if got := dirSize(root); got != 750 {
		t.Errorf("dirSize = %d, want 750", got)
	}
	if got := dirSize(filepath.Join(root, "nope")); got != 0 {
		t.Errorf("missing dir = %d, want 0", got)
	}
}

func TestScanTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "small", "x"), 10)
	writeFile(t, filepath.Join(root, "big", "y"), 1000)
	writeFile(t, filepath.Join(root, "loose"), 5) // file directly under root

	e, ok := scanTree("group", root, 1)
	if !ok {
		t.Fatal("scanTree returned not-ok for a real dir")
	}
	if e.Bytes != 1015 {
		t.Errorf("group total = %d, want 1015", e.Bytes)
	}
	// Children are immediate subdirs only, biggest first.
	if len(e.Children) != 2 {
		t.Fatalf("children = %d, want 2 (small, big)", len(e.Children))
	}
	if e.Children[0].Label != "big" || e.Children[0].Bytes != 1000 {
		t.Errorf("first child = %+v, want big/1000 (sorted desc)", e.Children[0])
	}

	// depth 0 sizes but does not break down.
	flat, _ := scanTree("g", root, 0)
	if len(flat.Children) != 0 || flat.Bytes != 1015 {
		t.Errorf("depth-0 = %+v, want sized with no children", flat)
	}

	if _, ok := scanTree("f", filepath.Join(root, "loose"), 1); ok {
		t.Error("scanTree on a file should return ok=false")
	}
}

func TestLabel(t *testing.T) {
	if got := label("objects"); got != "content store (built images)" {
		t.Errorf("known label = %q", got)
	}
	if got := label("custom-dir"); got != "custom-dir" {
		t.Errorf("unknown label should pass through, got %q", got)
	}
}
