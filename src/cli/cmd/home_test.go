package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A writable HOME is kept; an unset or missing one is replaced by a private one.
func TestEnsureWritableHome(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	good := t.TempDir()
	t.Setenv("HOME", good)
	ensureWritableHome()
	if os.Getenv("HOME") != good {
		t.Fatalf("a writable HOME must be kept, got %q", os.Getenv("HOME"))
	}

	t.Setenv("HOME", filepath.Join(t.TempDir(), "missing", "home"))
	ensureWritableHome()
	if h := os.Getenv("HOME"); !dirWritable(h) || !ownedPrivateDir(h) {
		t.Fatalf("a missing HOME must become a private writable one, got %q", h)
	}

	t.Setenv("HOME", "")
	ensureWritableHome()
	if h := os.Getenv("HOME"); h == "" || !dirWritable(h) {
		t.Fatalf("an unset HOME must become a private writable one, got %q", h)
	}
}

// A read-only HOME is kept for what it holds; only unset write targets move.
func TestEnsureWritableHome_ReadOnlyKeepsHome(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through read-only permissions")
	}
	t.Setenv("TMPDIR", t.TempDir())
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	t.Setenv("HOME", ro)
	t.Setenv("XDG_CACHE_HOME", "/kept")
	t.Setenv("BUILDX_CONFIG", "")
	os.Unsetenv("BUILDX_CONFIG")

	ensureWritableHome()
	if os.Getenv("HOME") != ro {
		t.Fatalf("a read-only HOME must be kept, got %q", os.Getenv("HOME"))
	}
	if os.Getenv("XDG_CACHE_HOME") != "/kept" {
		t.Errorf("a set XDG_CACHE_HOME must be kept, got %q", os.Getenv("XDG_CACHE_HOME"))
	}
	if b := os.Getenv("BUILDX_CONFIG"); !strings.HasPrefix(b, os.TempDir()) {
		t.Errorf("BUILDX_CONFIG = %q, want it under the private home", b)
	}
}

// The private home is refused when another user could have planted it: a symlink, or
// a directory open to others.
func TestPrivateHome_RefusesPlanted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	want := filepath.Join(tmp, "stagefreight-home-"+strconv.Itoa(os.Getuid()))

	if err := os.Symlink(t.TempDir(), want); err != nil {
		t.Fatal(err)
	}
	if got := privateHome(); got == want || !ownedPrivateDir(got) {
		t.Fatalf("symlinked home: got %q, want a fresh private dir", got)
	}
	os.Remove(want)

	if err := os.Mkdir(want, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(want, 0o777); err != nil {
		t.Fatal(err)
	}
	if got := privateHome(); got == want || !ownedPrivateDir(got) {
		t.Fatalf("world-writable home: got %q, want a fresh private dir", got)
	}
	os.Remove(want)

	if got := privateHome(); got != want {
		t.Fatalf("clean tmp: got %q, want %q", got, want)
	}
}
