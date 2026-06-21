package dependency

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindCargoUpdateDir(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[workspace]\nmembers = [\"crates/a\"]\n"), 0o644)
	mk := func(rel, body string) {
		os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755)
		os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644)
	}
	mk("crates/a/Cargo.toml", "[package]\nname=\"a\"\n")  // workspace member
	mk("patches/p/Cargo.toml", "[package]\nname=\"p\"\n") // [patch] path crate (the bug)

	for _, rel := range []string{"crates/a/Cargo.toml", "patches/p/Cargo.toml", "Cargo.toml"} {
		if got := findCargoUpdateDir(root, rel); got != root {
			t.Errorf("findCargoUpdateDir(%q) = %q, want workspace root %q", rel, got, root)
		}
	}

	// A standalone crate with no [workspace] ancestor updates in its own directory.
	solo := t.TempDir()
	os.MkdirAll(filepath.Join(solo, "tool"), 0o755)
	os.WriteFile(filepath.Join(solo, "tool", "Cargo.toml"), []byte("[package]\nname=\"tool\"\n"), 0o644)
	want := filepath.Join(solo, "tool")
	if got := findCargoUpdateDir(solo, "tool/Cargo.toml"); got != want {
		t.Errorf("standalone crate = %q, want %q", got, want)
	}
}

func TestCargoDeclaresWorkspace(t *testing.T) {
	if !cargoDeclaresWorkspace([]byte("[package]\nname=\"x\"\n[workspace]\n")) {
		t.Error("[workspace] table → true")
	}
	if !cargoDeclaresWorkspace([]byte("[workspace.package]\nversion=\"1\"\n")) {
		t.Error("[workspace.package] → true")
	}
	// A member that only uses workspace-inherited deps is NOT a workspace root.
	if cargoDeclaresWorkspace([]byte("[dependencies]\nserde = { workspace = true }\n")) {
		t.Error("`workspace = true` dep is not a [workspace] table")
	}
}
