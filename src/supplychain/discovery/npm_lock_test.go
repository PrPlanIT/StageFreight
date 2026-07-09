package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNpmLockResolution(t *testing.T) {
	dir := t.TempDir()
	lock := `{
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root"},
    "node_modules/postcss": {"version": "8.5.15"},
    "node_modules/@scope/pkg": {"version": "2.1.0"},
    "node_modules/postcss/node_modules/nanoid": {"version": "3.0.0"}
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	m := loadNpmLockVersions(filepath.Join(dir, "package-lock.json"))
	if m["postcss"] != "8.5.15" {
		t.Errorf("postcss = %q, want 8.5.15 (patched-in-lock, must not flag)", m["postcss"])
	}
	if m["@scope/pkg"] != "2.1.0" {
		t.Errorf("@scope/pkg = %q, want 2.1.0 (scope preserved)", m["@scope/pkg"])
	}
	if _, ok := m["nanoid"]; ok {
		t.Error("transitive node_modules/postcss/node_modules/nanoid must NOT be treated as top-level")
	}
}

func TestNpmLockResolution_V1(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package-lock.json"),
		[]byte(`{"lockfileVersion": 1, "dependencies": {"lodash": {"version": "4.17.21"}}}`), 0o644)
	m := loadNpmLockVersions(filepath.Join(dir, "package-lock.json"))
	if m["lodash"] != "4.17.21" {
		t.Errorf("v1 lodash = %q, want 4.17.21", m["lodash"])
	}
}
