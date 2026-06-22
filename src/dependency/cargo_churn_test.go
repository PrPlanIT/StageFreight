package dependency

import "testing"

func TestCountLockMutations(t *testing.T) {
	out := "    Updating crates.io index\n" +
		"    Updating git repository `https://github.com/x/y`\n" +
		"    Updating foo v1.0.0 -> v1.0.1\n" +
		"      Adding bar v2.0.0\n" +
		"    Removing baz v0.1.0\n" +
		"    Downgrading qux v3.0.0 -> v2.9.0\n" +
		"   Locking 4 packages to latest compatible versions\n"
	// Counts foo, bar, baz, qux (4) — never the index/git-repository refresh or the
	// "Locking N" summary line.
	if got := countLockMutations([]byte(out)); got != 4 {
		t.Errorf("countLockMutations = %d, want 4", got)
	}
	if got := countLockMutations([]byte("    Updating crates.io index\n")); got != 0 {
		t.Errorf("index-only output should count 0, got %d", got)
	}
}

func TestAppendUniqueStr(t *testing.T) {
	xs := appendUniqueStr(nil, "ureq")
	xs = appendUniqueStr(xs, "serde")
	xs = appendUniqueStr(xs, "ureq") // duplicate — ignored
	if len(xs) != 2 || xs[0] != "ureq" || xs[1] != "serde" {
		t.Errorf("appendUniqueStr = %v, want [ureq serde]", xs)
	}
}
