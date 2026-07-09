package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

func TestCargoLockResolution(t *testing.T) {
	dir := t.TempDir()
	lock := `# auto-generated
[[package]]
name = "tar"
version = "0.4.46"

[[package]]
name = "rand"
version = "0.8.6"

[[package]]
name = "rand"
version = "0.9.4"

[[package]]
name = "openssl"
version = "0.10.81"
`
	if err := os.WriteFile(filepath.Join(dir, "Cargo.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	m := loadCargoLockVersions(filepath.Join(dir, "Cargo.lock"))
	if m == nil {
		t.Fatal("expected parsed lock")
	}

	// Each manifest constraint resolves to the locked version that satisfies its caret.
	cases := []struct{ name, declared, want string }{
		{"tar", "0.4", "0.4.46"},       // loose pin, patched in lock
		{"rand", "0.8", "0.8.6"},       // multi-version lock: ^0.8 picks 0.8.6, not 0.9.4
		{"rand", "0.9", "0.9.4"},       // ^0.9 picks 0.9.4
		{"openssl", "0.10", "0.10.81"}, // patched
	}
	for _, c := range cases {
		got := version.LatestEligibleSemver(c.declared, m[c.name])
		if got != c.want {
			t.Errorf("%s declared %q → resolved %q, want %q (locked: %v)", c.name, c.declared, got, c.want, m[c.name])
		}
	}
}

func TestFindNearestFile_WorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte("# lock"), 0o644)
	member := filepath.Join(root, "crates", "dragonfly-server", "src")
	if err := os.MkdirAll(member, 0o755); err != nil {
		t.Fatal(err)
	}
	got := findNearestFile(member, "Cargo.lock")
	if got != filepath.Join(root, "Cargo.lock") {
		t.Errorf("findNearestFile = %q, want workspace-root lock", got)
	}
	if findNearestFile(member, "nonexistent.toml") != "" {
		t.Error("expected empty for missing file")
	}
}
