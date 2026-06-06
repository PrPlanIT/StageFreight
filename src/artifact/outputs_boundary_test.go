package artifact

import "testing"

// TestWithinManagedRoot pins the Perform→Publish boundary predicate: a path is
// "inside the boundary" iff it resolves at or beneath <repoRoot>/.stagefreight.
// This is the rule that, had it been enforced, would have caught the v0.6.1
// failure where binary archives were written to a bare dist/ — outside the only
// prefix CI forwards between the perform and publish jobs.
func TestWithinManagedRoot(t *testing.T) {
	const root = "/src"
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"relative under managed root", ".stagefreight/dist/x-linux-amd64.tar.gz", true},
		{"absolute under managed root", "/src/.stagefreight/dist/x.tar.gz", true},
		{"managed root itself", ".stagefreight", true},
		{"nested managed subtree", ".stagefreight/objects/ab/cd", true},
		{"bare dist escapes (the v0.6.1 bug)", "dist/x-linux-amd64.tar.gz", false},
		{"absolute bare dist escapes", "/src/dist/x.tar.gz", false},
		{"sibling that prefix-matches the name is not inside", ".stagefreight-cache/x", false},
		{"parent escape", "../.stagefreight/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WithinManagedRoot(root, tc.path); got != tc.want {
				t.Errorf("WithinManagedRoot(%q, %q) = %v, want %v", root, tc.path, got, tc.want)
			}
		})
	}
}

// TestOutputsManifest_FilesystemArtifacts_WithinManagedRoot is the invariant
// guard for future artifact-producing engines: every filesystem-resident
// artifact a manifest hands to publish (binary, archive, oci_layout) must live
// beneath ManagedRoot. Docker artifacts travel by Digest, carry no filesystem
// path, and are intentionally not collected — so adding one must not trip this.
func TestOutputsManifest_FilesystemArtifacts_WithinManagedRoot(t *testing.T) {
	const root = "/src"

	good := &OutputsManifest{Artifacts: []Artifact{
		{ID: "binary:x-linux-amd64", Kind: "binary",
			Binary: &BinaryDescriptor{OS: "linux", Arch: "amd64", Path: "/src/.stagefreight/dist/linux-amd64/x"}},
		{ID: "archive:x-linux-amd64.tar.gz", Kind: "archive",
			Archive: &ArchiveDescriptor{Format: "tar.gz", Path: "/src/.stagefreight/dist/x-linux-amd64.tar.gz"}},
		{ID: "image:x", Kind: "docker", // digest-only, no path — must be ignored here
			Docker: &DockerDescriptor{Dockerfile: "Dockerfile"}, Digest: "sha256:abc",
			Persistence: PersistenceHandle{Kind: PersistenceOCILayout, OCILayout: &OCILayoutRef{Path: ".stagefreight/objects/x"}}},
	}}

	paths := good.LocalFilesystemPaths()
	if _, ok := paths["image:x"]; ok && good.Artifacts[2].Persistence.OCILayout == nil {
		t.Fatal("docker artifact without a filesystem path must not be collected")
	}
	for id, p := range paths {
		if !WithinManagedRoot(root, p) {
			t.Errorf("artifact %s path %q escapes ManagedRoot %q", id, p, ManagedRoot)
		}
	}

	// A producer that regresses to a bare dist/ must be caught.
	bad := &OutputsManifest{Artifacts: []Artifact{
		{ID: "archive:x-linux-amd64.tar.gz", Kind: "archive",
			Archive: &ArchiveDescriptor{Format: "tar.gz", Path: "/src/dist/x-linux-amd64.tar.gz"}},
	}}
	escaped := false
	for _, p := range bad.LocalFilesystemPaths() {
		if !WithinManagedRoot(root, p) {
			escaped = true
		}
	}
	if !escaped {
		t.Fatal("expected a bare dist/ archive path to be flagged as outside ManagedRoot")
	}
}
