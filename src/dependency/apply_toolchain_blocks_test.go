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
	if v, k, s := findToolBlockLines(lines, 2, 4, 6); v != 3 || k != "version" || s != 4 {
		t.Errorf("cargo-llvm-cov: verIdx=%d key=%q shaIdx=%d, want 3,version,4", v, k, s)
	}
	// trivy block (key at line 5) has only a version line — no digest to touch.
	if v, k, s := findToolBlockLines(lines, 5, 4, 7); v != 6 || k != "version" || s != -1 {
		t.Errorf("trivy: verIdx=%d key=%q shaIdx=%d, want 6,version,-1", v, k, s)
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
