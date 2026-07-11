package dependency

import (
	"strings"
	"testing"
)

func TestFindToolConstraintLine(t *testing.T) {
	lines := strings.Split(`toolchains:
  desired:
    cargo-llvm-cov:
      version: "0.8.7"
    trivy:
      version: "0.69.x"
    empty:
      other: 1
`, "\n")

	// cargo-llvm-cov block (key at line 2, indent 4): the `version:` line.
	if v, k := findToolConstraintLine(lines, 2, 4, 8); v != 3 || k != "version" {
		t.Errorf("cargo-llvm-cov: verIdx=%d key=%q, want 3,version", v, k)
	}
	// trivy block (key at line 4): a wildcard version.
	if v, k := findToolConstraintLine(lines, 4, 4, 8); v != 5 || k != "version" {
		t.Errorf("trivy: verIdx=%d key=%q, want 5,version", v, k)
	}
	// A block with no version line → not found.
	if v, k := findToolConstraintLine(lines, 6, 4, 8); v != -1 || k != "" {
		t.Errorf("empty: verIdx=%d key=%q, want -1,''", v, k)
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
