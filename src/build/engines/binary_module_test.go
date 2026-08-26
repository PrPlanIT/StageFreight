package engines

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// TestResolveTemplateVars_ProjectModule proves {project.module} resolves in binary
// build args (ldflags) from the repo's build manifest — the headline that lets one
// shared build preset serve a whole bucket. Detection is gated on RootDir.
func TestResolveTemplateVars_ProjectModule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := build.BuildConfig{RootDir: dir, Version: &build.VersionInfo{Version: "1.2.3", SHA: "abcdef1234567"}}

	got := resolveTemplateVars("-X {project.module}/src/version.Version={version}", cfg)
	want := "-X example.com/tool/src/version.Version=1.2.3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Without a RootDir the token is left literal (no manifest to read) rather than
	// resolving to empty — a missing rootDir must not silently blank the module path.
	cfgNoRoot := build.BuildConfig{Version: &build.VersionInfo{Version: "1"}}
	if got := resolveTemplateVars("{project.module}", cfgNoRoot); got != "{project.module}" {
		t.Errorf("no rootDir: got %q, want literal token", got)
	}
}
