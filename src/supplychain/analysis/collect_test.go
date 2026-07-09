package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsDominatedLockfile — relocated from the former osv lint module. A nested
// lockfile under an ancestor of the same name is dominated (skipped); the
// top-level build lockfile is not.
func TestIsDominatedLockfile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Cargo.lock"), []byte("# root"), 0o644)
	sub := filepath.Join(root, "crates", "rqlite-rs")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sub, "Cargo.lock"), []byte("# vendored sub-lock"), 0o644)

	if isDominatedLockfile(filepath.Join(root, "Cargo.lock"), "Cargo.lock") {
		t.Error("root Cargo.lock must NOT be dominated — it is the build graph")
	}
	if !isDominatedLockfile(filepath.Join(sub, "Cargo.lock"), "Cargo.lock") {
		t.Error("nested vendored Cargo.lock must be dominated (skipped)")
	}
}

// TestScoreToLabel verifies the osv-scanner numeric-score → OSV-label mapping
// reproduces the former severityFromScore tiers once run through evaluate.
func TestScoreToLabel(t *testing.T) {
	cases := map[string]Verdict{
		"9.8": VerdictCritical, // CRITICAL
		"7.5": VerdictCritical, // HIGH → critical (matches severityFromScore ≥7)
		"5.0": VerdictWarning,  // MODERATE
		"2.0": VerdictInfo,     // LOW
		"0":   VerdictInfo,     // UNKNOWN
		"":    VerdictWarning,  // unparseable → MODERATE (severityFromScore default)
	}
	for score, want := range cases {
		got := severityVerdict(scoreToLabel(score))
		if got != want {
			t.Errorf("score %q → label %q → verdict %v, want %v", score, scoreToLabel(score), got, want)
		}
	}
}
