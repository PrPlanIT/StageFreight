//go:build integration

package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveNode_RunsOnMusl provisions node from scratch on a musl host (run it in the
// StageFreight runtime image, which ships no libstdc++/libgcc) and proves node and npm
// execute with only the resolved Result.Env — then that a cache hit re-verifies the
// libraries, and that a tree whose libraries are gone is repaired rather than served.
func TestResolveNode_RunsOnMusl(t *testing.T) {
	if !isMuslLibc() {
		t.Skip("musl host only")
	}
	root := t.TempDir()

	run := func(t *testing.T, r Result) {
		t.Helper()
		for _, args := range [][]string{{filepath.Join(filepath.Dir(r.Path), "node"), "--version"}, {filepath.Join(filepath.Dir(r.Path), "npm"), "--version"}} {
			cmd := exec.Command(args[0], args[1:]...)
			cmd.Env = append(append(CleanEnv(), "PATH="+filepath.Dir(r.Path)), r.Env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
			}
			t.Logf("%s → %s", filepath.Base(args[0]), strings.TrimSpace(string(out)))
		}
	}

	fresh, err := Resolve(root, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.CacheHit || len(fresh.Env) == 0 {
		t.Fatalf("fresh install: hit=%v env=%v", fresh.CacheHit, fresh.Env)
	}
	run(t, fresh)

	hit, err := Resolve(root, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	if !hit.CacheHit {
		t.Fatal("second resolve should be a cache hit")
	}
	run(t, hit)

	libDir := strings.TrimPrefix(fresh.Env[0], "LD_LIBRARY_PATH=")
	if err := os.RemoveAll(libDir); err != nil {
		t.Fatal(err)
	}
	repaired, err := Resolve(root, "node", "")
	if err != nil {
		t.Fatal(err)
	}
	run(t, repaired)
}
