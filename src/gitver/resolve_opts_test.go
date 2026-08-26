package gitver

import "testing"

// TestResolveTemplateWithOpts_ProjectDescription pins the inversion of the former
// projectDescription package-global: {project.description} now resolves from the
// caller-injected ResolveOptions, and the plain wrapper (no opts) resolves it to
// empty — with no hidden package state either way.
func TestResolveTemplateWithOpts_ProjectDescription(t *testing.T) {
	v := &VersionInfo{Version: "1.0.0"}
	// {project.*} resolution is gated on a non-empty rootDir; a temp dir enables it
	// while keeping the git-derived fields empty and the test deterministic.
	dir := t.TempDir()

	got := ResolveTemplateWithOpts("d={project.description}", v, dir, nil, ResolveOptions{ProjectDescription: "does things"})
	if got != "d=does things" {
		t.Errorf("injected description: got %q, want %q", got, "d=does things")
	}

	// The plain entry point injects no options → {project.description} is empty,
	// deterministically, regardless of any prior call (no global to carry state).
	got2 := ResolveTemplateWithDirAndVars("d={project.description}", v, dir, nil)
	if got2 != "d=" {
		t.Errorf("no injected description: got %q, want %q", got2, "d=")
	}
}
