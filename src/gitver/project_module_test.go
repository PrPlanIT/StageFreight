package gitver

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectModule covers the builder-dispatched {project.module} fact: the canonical
// module/package name read from whichever build manifest is present. This is the value
// that makes a shared build preset serve a whole bucket (go AND rust AND node), so all
// four manifests are exercised.
func TestDetectModule(t *testing.T) {
	cases := []struct{ file, content, want string }{
		{"go.mod", "module github.com/PrPlanIT/StageFreight\n\ngo 1.26\n", "github.com/PrPlanIT/StageFreight"},
		{"Cargo.toml", "[package]\nname = \"dragonfly\"\nversion = \"0.1.0\"\n", "dragonfly"},
		{"package.json", "{\n  \"name\": \"fairer-pages\",\n  \"version\": \"1.0.0\"\n}\n", "fairer-pages"},
		{"pyproject.toml", "[build-system]\nrequires = []\n\n[project]\nname = \"mytool\"\n", "mytool"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, c.file), []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := detectModule(dir); got != c.want {
			t.Errorf("%s: detectModule = %q, want %q", c.file, got, c.want)
		}
	}

	// No recognized manifest → empty (not a crash, not a stray literal).
	if got := detectModule(t.TempDir()); got != "" {
		t.Errorf("no manifest: detectModule = %q, want empty", got)
	}
}

// TestDetectModule_TomlKeyPrecision guards the hand TOML parse against matching a key
// that merely starts with "name" (e.g. "namespace") or a name outside the target
// section.
func TestDetectModule_TomlKeyPrecision(t *testing.T) {
	dir := t.TempDir()
	// name lives under [tool.other], not [package]; [package] has only namespace.
	content := "[tool.other]\nname = \"wrong\"\n\n[package]\nnamespace = \"nope\"\nname = \"right\"\n"
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tomlSectionName(filepath.Join(dir, "Cargo.toml"), "package"); got != "right" {
		t.Errorf("tomlSectionName = %q, want %q", got, "right")
	}
}

// TestResolveProjectModule proves the headline: {project.module} resolves through the
// leaf pass so a shared ldflags template (…/{project.module}/src/version…) expands to
// the repo's own module path.
func TestResolveProjectModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo/bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveTemplateWithOpts("{project.module}/src/version", &VersionInfo{Version: "1"}, dir, nil, ResolveOptions{})
	if got != "example.com/foo/bar/src/version" {
		t.Errorf("resolve {project.module} = %q, want %q", got, "example.com/foo/bar/src/version")
	}
}
