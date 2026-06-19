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
		{".stagefreight/badges/build.svg", false}, // persistent carve-out
		{".stagefreight/.gitignore", false},       // managed, stays tracked
		{"src/main.go", false},                    // user source — never
		{"deps/x", false},                         // outside the namespace
		{".stagefreight/reportsX/y", false},       // not the reports/ dir
	}
	for _, c := range cases {
		if got := IsEphemeral(c.path); got != c.want {
			t.Errorf("IsEphemeral(%q)=%v want %v", c.path, got, c.want)
		}
	}
}

func TestGitignoreManaged(t *testing.T) {
	g := gitignoreManaged()
	for _, want := range []string{"/deps/", "/reports/", "/security/", "/dist/", "/pipeline.json"} {
		if !strings.Contains(g, want) {
			t.Errorf("managed gitignore missing %q:\n%s", want, g)
		}
	}
	if strings.Contains(g, "/badges") {
		t.Errorf("persistent badges/ must NOT have an ignore rule:\n%s", g)
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
