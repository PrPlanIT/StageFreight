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
`, "\n")

	// cargo-llvm-cov block (key at line 2, indent 4) has both version + sha256.
	if v, s := findToolBlockLines(lines, 2, 4, 6); v != 3 || s != 4 {
		t.Errorf("cargo-llvm-cov: verIdx=%d shaIdx=%d, want 3,4", v, s)
	}
	// trivy block (key at line 5) has only a version line — no digest to touch.
	if v, s := findToolBlockLines(lines, 5, 4, 7); v != 6 || s != -1 {
		t.Errorf("trivy: verIdx=%d shaIdx=%d, want 6,-1", v, s)
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
