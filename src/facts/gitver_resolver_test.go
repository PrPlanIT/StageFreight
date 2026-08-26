package facts

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// TestGitverLeaf_ResolvesAndInjectsDescription verifies the adapter resolves gitver
// leaf tokens from the Context and injects the config-sourced description through
// ResolveOptions (no package global).
func TestGitverLeaf_ResolvesAndInjectsDescription(t *testing.T) {
	res := GitverLeaf()

	got := res.Resolve([]string{"v{version}"}, &Context{Version: &gitver.VersionInfo{Version: "1.2.3"}})
	if got[0] != "v1.2.3" {
		t.Errorf("version: got %q, want %q", got[0], "v1.2.3")
	}

	// {project.*} needs a non-empty RootDir; a temp dir enables the family while
	// keeping git-derived fields empty, so only the injected description is asserted.
	dir := t.TempDir()
	gotDesc := res.Resolve([]string{"d={project.description}"}, &Context{
		Version:     &gitver.VersionInfo{Version: "1.2.3"},
		RootDir:     dir,
		Description: "does things",
	})
	if gotDesc[0] != "d=does things" {
		t.Errorf("description: got %q, want %q", gotDesc[0], "d=does things")
	}
}

// TestGitverLeaf_NoVersionIsNoOp verifies a nil Version (or nil Context) leaves the
// text untouched rather than panicking.
func TestGitverLeaf_NoVersionIsNoOp(t *testing.T) {
	res := GitverLeaf()
	if got := res.Resolve([]string{"v{version}"}, &Context{}); got[0] != "v{version}" {
		t.Errorf("nil version: got %q, want %q", got[0], "v{version}")
	}
	if got := res.Resolve([]string{"v{version}"}, nil); got[0] != "v{version}" {
		t.Errorf("nil context: got %q, want %q", got[0], "v{version}")
	}
}
