package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsEphemeral(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".stagefreight/pipeline.json", true},
		{".stagefreight/reports/lint.xml", true},
		{".stagefreight/deps/report.json", true},
		{".stagefreight/security/sbom.json", true},
		{".stagefreight/dist/jetpack.tar.gz", true},
		{".stagefreight/anything-new.json", true},  // inverted: unknown output → ephemeral by default
		{".stagefreight/reportsX/y", true},         // not a durable entry → ephemeral (was false under enumerate)
		{".stagefreight/badges/build.svg", false},  // durable carve-out
		{".stagefreight/preset-cache/x.yml", false}, // durable carve-out
		{".stagefreight/toolchains.lock", false},   // durable carve-out
		{".stagefreight/.gitignore", false},        // managed, stays tracked
		{"src/main.go", false},                     // user source — never
		{"deps/x", false},                          // outside the namespace
	}
	for _, c := range cases {
		if got := IsEphemeral(c.path); got != c.want {
			t.Errorf("IsEphemeral(%q)=%v want %v", c.path, got, c.want)
		}
	}
}

func TestGitignoreManaged(t *testing.T) {
	g := gitignoreManaged()
	// Allowlist body: ignore everything, then re-include the durable set.
	for _, want := range []string{"/*", "!/.gitignore", "!/badges/", "!/preset-cache/", "!/toolchains.lock"} {
		if !strings.Contains(g, want) {
			t.Errorf("managed gitignore missing %q:\n%s", want, g)
		}
	}
	// Ephemeral outputs must NOT be enumerated — they are covered by /* and stay ignored.
	for _, absent := range []string{"/deps/", "/reports/", "/pipeline.json"} {
		if strings.Contains(g, absent) {
			t.Errorf("ephemeral entry %q must not be enumerated (covered by /*):\n%s", absent, g)
		}
	}
}

// TestDurableAllowlistComplete is the load-bearing invariant of the inverted model:
// every durable entry must be carved out of BOTH the ephemeral classifier and the
// managed ignore body. A missed entry is silently ignored AND untracked, so this ties
// the allowlist to both mechanisms — add a durable file to persistentEntries and it is
// checked automatically.
func TestDurableAllowlistComplete(t *testing.T) {
	g := gitignoreManaged()
	for _, p := range persistentEntries {
		probe := NamespaceDir + "/" + p
		if strings.HasSuffix(p, "/") {
			probe += "probe" // a file inside the durable directory
		}
		if IsEphemeral(probe) {
			t.Errorf("durable entry %q classified ephemeral (probe %q)", p, probe)
		}
		if !strings.Contains(g, "!/"+p) {
			t.Errorf("durable entry %q missing from managed gitignore allowlist", p)
		}
	}
}

func TestEnsureGitignore(t *testing.T) {
	root := t.TempDir()
	changed, err := EnsureGitignore(root)
	if err != nil || !changed {
		t.Fatalf("first EnsureGitignore: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(GitignorePath(root)); err != nil {
		t.Fatalf("gitignore not written: %v", err)
	}
	if changed, err := EnsureGitignore(root); err != nil || changed {
		t.Fatalf("second EnsureGitignore must be a no-op: changed=%v err=%v", changed, err)
	}
	// The managed file lives INSIDE the namespace, never at repo root.
	if filepath.Base(filepath.Dir(GitignorePath(root))) != NamespaceDir {
		t.Errorf("gitignore must live inside %s", NamespaceDir)
	}
}

// The scope boundary is enforced in code: refuse anything outside the SF
// ephemeral namespace BEFORE any git mutation (so it needs no repo).
func TestUntrackEphemeral_RefusesOutsideNamespace(t *testing.T) {
	root := t.TempDir()
	if err := UntrackEphemeral(root, []string{"src/main.go"}); err == nil ||
		!strings.Contains(err.Error(), "outside the StageFreight ephemeral namespace") {
		t.Fatalf("expected scope refusal for user source, got %v", err)
	}
	if err := UntrackEphemeral(root, []string{".stagefreight/badges/build.svg"}); err == nil {
		t.Fatalf("expected refusal for persistent badges/, got nil")
	}
	if err := UntrackEphemeral(root, nil); err != nil {
		t.Fatalf("empty set must be a no-op, got %v", err)
	}
}
