package dependency

import (
	"strings"
	"testing"
)

func TestFindToolBlockLines(t *testing.T) {
	lines := strings.Split(`toolchains:
  desired:
    cargo-llvm-cov:
      version: "0.8.7"
      sha256: "abc123"
    trivy:
      version: "0.69.3"
    kubectl:
      constraint: "1.26.x"
      resolved: "1.26.7"
      sha256: "def456"
`, "\n")

	// cargo-llvm-cov block (key at line 2, indent 4) has version + sha256, no resolved.
	if v, k, r, s := findToolBlockLines(lines, 2, 4, 6); v != 3 || k != "version" || r != -1 || s != 4 {
		t.Errorf("cargo-llvm-cov: verIdx=%d key=%q resolvedIdx=%d shaIdx=%d, want 3,version,-1,4", v, k, r, s)
	}
	// trivy block (key at line 5) has only a version line — no digest to touch.
	if v, k, r, s := findToolBlockLines(lines, 5, 4, 7); v != 6 || k != "version" || r != -1 || s != -1 {
		t.Errorf("trivy: verIdx=%d key=%q resolvedIdx=%d shaIdx=%d, want 6,version,-1,-1", v, k, r, s)
	}
	// kubectl block (key at line 7) is a wildcard lock: constraint + resolved + sha256.
	if v, k, r, s := findToolBlockLines(lines, 7, 4, 10); v != 8 || k != "constraint" || r != 9 || s != 10 {
		t.Errorf("kubectl: verIdx=%d key=%q resolvedIdx=%d shaIdx=%d, want 8,constraint,9,10", v, k, r, s)
	}
}

func TestInsertLine(t *testing.T) {
	base := []string{"a", "b", "c"}
	got := insertLine(base, 1, "X")
	if strings.Join(got, ",") != "a,X,b,c" {
		t.Errorf("insert at 1 = %v, want [a X b c]", got)
	}
	// The source slice must be untouched (fresh backing array).
	if strings.Join(base, ",") != "a,b,c" {
		t.Errorf("insertLine mutated source: %v", base)
	}
	// Append at the end.
	if end := insertLine(base, len(base), "Z"); strings.Join(end, ",") != "a,b,c,Z" {
		t.Errorf("insert at end = %v, want [a b c Z]", end)
	}
}

func TestLeadIndent(t *testing.T) {
	if got := leadIndent(`      version: "1"`); got != "      " {
		t.Errorf("leadIndent = %q, want 6 spaces", got)
	}
	if got := leadIndentWidth("    trivy:"); got != 4 {
		t.Errorf("leadIndentWidth = %d, want 4", got)
	}
}
