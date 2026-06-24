package render

import (
	"os"
	"path"
	"slices"
	"strings"
	"testing"
)

// The artifact-handoff contract.
//
// StageFreight's phase spine hands `.stagefreight/` (the logical state root — cistate,
// content store, reports) from one job to the next as a CI artifact. The runtime always
// reads it at the canonical path (`rootDir/.stagefreight/…`) and never infers location
// from forge behavior. For that to hold, every forge's artifact upload→download round
// trip MUST reconstruct `.stagefreight/` at that canonical path — regardless of how the
// forge's artifact mechanism mangles paths.
//
// This was an *implicit* assumption ("artifact transport preserves path identity") that
// GitLab happened to satisfy and GitHub does not. These tests MODEL each forge's
// documented artifact semantics and assert the reconstruction, so a regression — or a
// new forge with different semantics — fails here as a unit test instead of being
// discovered one layer at a time in a production CI run.

// actionsUpload models actions/upload-artifact: files are stored relative to the
// least-common-ancestor of the search path. For a single directory path like
// ".stagefreight/", the LCA *is* that directory, so the artifact carries its CONTENTS
// with the prefix stripped (e.g. "pipeline.json", not ".stagefreight/pipeline.json").
func actionsUpload(uploadPath string, files []string) []string {
	root := strings.TrimSuffix(uploadPath, "/")
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(f, root), "/"))
	}
	return out
}

// actionsDownload models actions/download-artifact: a single named artifact's contents
// are extracted INTO the given path (no artifact-name subdir).
func actionsDownload(downloadPath string, artifactFiles []string) []string {
	out := make([]string, 0, len(artifactFiles))
	for _, f := range artifactFiles {
		out = append(out, path.Join(downloadPath, f))
	}
	return out
}

// gitlabRoundTrip models GitLab artifacts: declared paths are preserved verbatim — the
// identity transform StageFreight implicitly assumed everywhere.
func gitlabRoundTrip(files []string) []string { return files }

const (
	stateRoot = ".stagefreight/"
	cistate   = ".stagefreight/pipeline.json"
)

// TestArtifactHandoffContract_Actions locks the GitHub/Actions-family handoff: the
// rendered upload+download, run through the documented artifact semantics, must
// reconstruct the cistate at its canonical path. Reads the golden so a render change to
// the upload/download wiring breaks this test, not a CI run.
func TestArtifactHandoffContract_Actions(t *testing.T) {
	golden, err := os.ReadFile("testdata/github.golden.yml")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	g := string(golden)

	// Clause 1 — the dot-dir handoff is actually uploaded. upload-artifact v4.4.0+
	// excludes hidden paths (anything under a dot-dir) by default; `.stagefreight/` is one.
	if !strings.Contains(g, "include-hidden-files: true") {
		t.Fatal("CONTRACT: upload-artifact must set include-hidden-files: true — .stagefreight is a dot-dir and is otherwise silently dropped")
	}
	// Clause 2 — download reconstructs the root. upload strips the LCA prefix, so the
	// download must extract INTO .stagefreight (not "." which would flatten to root).
	if !strings.Contains(g, "path: .stagefreight\n") {
		t.Fatal("CONTRACT: download-artifact must use `path: .stagefreight` — upload stores contents relative to the LCA, so the prefix must be reconstructed on download")
	}

	// Clause 3 — model the round trip end-to-end and assert reconstruction.
	files := []string{cistate, ".stagefreight/reports/lint.xml", ".stagefreight/deps/resolve.json"}
	artifact := actionsUpload(stateRoot, files)
	for _, f := range artifact {
		if strings.HasPrefix(f, ".stagefreight/") {
			t.Fatalf("model invariant: artifact must NOT retain the .stagefreight/ prefix (LCA strip); got %q", f)
		}
	}
	restored := actionsDownload(".stagefreight", artifact)
	if !slices.Contains(restored, cistate) {
		t.Fatalf("CONTRACT VIOLATION: cistate did not reconstruct at %q; restored=%v", cistate, restored)
	}

	// Negative control — the pre-fix `path: .` flattens the handoff to the workspace
	// root, which is exactly why assertAuditionRan missed it. Documents *why* we use .stagefreight.
	if slices.Contains(actionsDownload(".", artifact), cistate) {
		t.Fatal("download into '.' must FLATTEN the handoff (cistate at root, not under .stagefreight) — this is the bug `path: .stagefreight` fixes")
	}
}

// TestArtifactHandoffContract_GitLab locks the invariant for GitLab, where the artifact
// mechanism preserves the path — the round trip is identity and the cistate survives as-is.
func TestArtifactHandoffContract_GitLab(t *testing.T) {
	restored := gitlabRoundTrip([]string{cistate})
	if !slices.Contains(restored, cistate) {
		t.Fatalf("CONTRACT VIOLATION (gitlab): cistate not preserved through the round trip; got %v", restored)
	}
}
