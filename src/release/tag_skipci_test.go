package release

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMessageSkipsCI(t *testing.T) {
	skips := []string{
		"docs: refresh generated docs and badges [skip ci]",
		"chore: bump [ci skip]",
		"tidy [no ci]",
		"generated skip-ci",
		"x [skip actions]",
	}
	for _, m := range skips {
		if !MessageSkipsCI(m) {
			t.Errorf("should be detected as CI-skipping: %q", m)
		}
	}
	keeps := []string{
		"feat: a real feature",
		"fix: correct a thing",
		"ci: add pipeline config", // 'ci' as a scope is NOT a skip marker
	}
	for _, m := range keeps {
		if MessageSkipsCI(m) {
			t.Errorf("should NOT be detected as CI-skipping: %q", m)
		}
	}
}

// TestResolveReleasableCommit_SkipsCITip: a release must never target a [skip ci] tip; it
// walks back to the nearest releasable ancestor and reports the skipped tip.
func TestResolveReleasableCommit_SkipsCITip(t *testing.T) {
	dir := t.TempDir()
	_, wt := initMainRepo(t, dir)
	// goCommit needs a dirty tree per commit (go-git rejects an empty commit).
	commit := func(file, msg string) string {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(file); err != nil {
			t.Fatalf("add %s: %v", file, err)
		}
		return goCommit(t, wt, msg).String()
	}
	real := commit("a.txt", "feat(build): a real feature")
	tip := commit("b.txt", "docs: refresh generated docs and badges [skip ci]")

	eff, skipped, err := resolveReleasableCommit(dir, tip)
	if err != nil {
		t.Fatalf("resolveReleasableCommit: %v", err)
	}
	if eff != real {
		t.Errorf("effective = %s, want the real commit %s (walked past the [skip ci] tip)", eff, real)
	}
	if skipped != tip {
		t.Errorf("skippedTip = %s, want the tip %s", skipped, tip)
	}

	// A releasable tip is returned unchanged, with no reported skip.
	eff2, skipped2, err := resolveReleasableCommit(dir, real)
	if err != nil {
		t.Fatal(err)
	}
	if eff2 != real || skipped2 != "" {
		t.Errorf("releasable tip: eff=%s skipped=%q, want %s / empty", eff2, skipped2, real)
	}
}

// A narrate tip carries the Generated-By trailer (not [skip ci]); the tagger must still
// walk past it. A deps tip carries Updated-By and IS releasable — it rebuilt the image.
func TestResolveReleasableCommit_TrailerTips(t *testing.T) {
	dir := t.TempDir()
	_, wt := initMainRepo(t, dir)
	commit := func(file, msg string) string {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Add(file); err != nil {
			t.Fatalf("add %s: %v", file, err)
		}
		return goCommit(t, wt, msg).String()
	}
	real := commit("a.txt", "feat(build): a real feature")
	narrate := commit("b.txt", "docs: refresh generated assets\n\nGenerated-By: StageFreight")

	eff, skipped, err := resolveReleasableCommit(dir, narrate)
	if err != nil {
		t.Fatalf("resolveReleasableCommit: %v", err)
	}
	if eff != real || skipped != narrate {
		t.Errorf("narrate tip: eff=%s skipped=%s, want %s / %s (walked past Generated-By)", eff, skipped, real, narrate)
	}

	// Updated-By (deps) rebuilds the image → it stays releasable.
	deps := commit("c.txt", "fix(deps): bump x\n\nUpdated-By: StageFreight")
	eff2, skipped2, err := resolveReleasableCommit(dir, deps)
	if err != nil {
		t.Fatal(err)
	}
	if eff2 != deps || skipped2 != "" {
		t.Errorf("deps tip: eff=%s skipped=%q, want %s / empty (Updated-By is releasable)", eff2, skipped2, deps)
	}
}
