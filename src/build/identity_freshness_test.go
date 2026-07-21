package build

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHashBuildContext_ChangesWithSource is the regression that would have caught
// the stale-binary ship: change a source byte with the SAME build shape, and the
// build-context content hash must change. When it didn't, a real code fix compiled
// to an identically-identified build and shipped the old bytes.
func TestHashBuildContext_ChangesWithSource(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	mustWrite(t, df, "FROM scratch\nCOPY . /\n")
	src := filepath.Join(dir, "main.go")
	mustWrite(t, src, "package main\nfunc main() {}\n")

	before := HashBuildContext(df, dir)

	// The fix scenario: same Dockerfile, same paths/args — only the code changed.
	mustWrite(t, src, "package main\nfunc main() { fixed() }\n")
	after := HashBuildContext(df, dir)

	if before == after {
		t.Fatal("HashBuildContext did not change when a source file changed — a code fix would ship as a stale build")
	}
}

// TestHashBuildContext_StableAndDockerfileSensitive: identical trees hash equal
// (reproducibility/crucible unaffected), and a Dockerfile change is also caught.
func TestHashBuildContext_StableAndDockerfileSensitive(t *testing.T) {
	mk := func() (string, string) {
		dir := t.TempDir()
		df := filepath.Join(dir, "Dockerfile")
		mustWrite(t, df, "FROM alpine\nRUN true\n")
		mustWrite(t, filepath.Join(dir, "a.go"), "package a\n")
		mustWrite(t, filepath.Join(dir, "sub", "b.go"), "package b\n")
		return df, dir
	}
	df1, d1 := mk()
	df2, d2 := mk()
	if HashBuildContext(df1, d1) != HashBuildContext(df2, d2) {
		t.Fatal("identical trees hashed differently — not deterministic")
	}
	// Dockerfile-only change is build-affecting and must register.
	mustWrite(t, df2, "FROM alpine\nRUN false\n")
	if HashBuildContext(df1, d1) == HashBuildContext(df2, d2) {
		t.Fatal("Dockerfile change not reflected in the context hash")
	}
}

// TestHashBuildContext_IgnoresNoise: .git / .stagefreight churn must not move the
// hash, or every run would look "changed" and defeat the point.
func TestHashBuildContext_IgnoresNoise(t *testing.T) {
	dir := t.TempDir()
	df := filepath.Join(dir, "Dockerfile")
	mustWrite(t, df, "FROM scratch\n")
	mustWrite(t, filepath.Join(dir, "main.go"), "package main\n")
	base := HashBuildContext(df, dir)

	mustWrite(t, filepath.Join(dir, ".git", "index"), "gitjunk")
	mustWrite(t, filepath.Join(dir, ".stagefreight", "pipeline.json"), "{}")
	if HashBuildContext(df, dir) != base {
		t.Fatal(".git/.stagefreight churn changed the build-context hash")
	}
}

// TestNormalizeBuildPlan_TracksContextDigest pins the fix at the identity layer:
// the plan fingerprint must vary with ContextDigest (source content), while staying
// deterministic for identical content. Before the fix this FAILED — the fingerprint
// ignored source entirely.
func TestNormalizeBuildPlan_TracksContextDigest(t *testing.T) {
	plan := func(cd string) *BuildPlan {
		return &BuildPlan{Steps: []BuildStep{{
			Name: "app", Dockerfile: "Dockerfile", Context: ".", ContextDigest: cd,
		}}}
	}
	if NormalizeBuildPlan(plan("source-A")) == NormalizeBuildPlan(plan("source-B")) {
		t.Fatal("plan fingerprint ignored ContextDigest — build identity is blind to source content")
	}
	if NormalizeBuildPlan(plan("source-A")) != NormalizeBuildPlan(plan("source-A")) {
		t.Fatal("fingerprint not deterministic for identical content")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
