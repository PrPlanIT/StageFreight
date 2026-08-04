package dependency

import (
	"strings"
	"testing"
)

// flatConfig is the flat toolchains: schema — scalar shorthand, block-map, and
// inline flow-map entry forms, with a trailing top-level section bounding it.
const flatConfig = `versioning:
  scheme: semver

# tool pins
toolchains:
  trivy: "0.69.3"
  syft: 1.42.3
  cargo-llvm-cov:
    version: "0.8.x"
  osv-scanner: {version: "2.3.5"}
  kubectl: "1.34.2" # pinned to the cluster line
  empty:
    other: 1

scribe:
  files: {}
`

func TestFindToolchainsSection(t *testing.T) {
	lines := strings.Split(flatConfig, "\n")

	start, end := findToolchainsSection(lines)
	if start != 5 || end != 13 {
		t.Fatalf("section = (%d,%d), want (5,13)", start, end)
	}

	// No toolchains section at all.
	if s, e := findToolchainsSection([]string{"versioning:", "  scheme: semver"}); s != -1 || e != -1 {
		t.Errorf("missing section = (%d,%d), want (-1,-1)", s, e)
	}

	// A nested identically-named key must not match — only the top-level section.
	nested := []string{"other:", "  toolchains:", "    trivy: \"1.0.0\""}
	if s, e := findToolchainsSection(nested); s != -1 || e != -1 {
		t.Errorf("nested-only = (%d,%d), want (-1,-1)", s, e)
	}
}

func TestFindToolEntry(t *testing.T) {
	lines := strings.Split(flatConfig, "\n")
	start, end := findToolchainsSection(lines)

	cases := []struct {
		tool       string
		verIdx     int
		verKey     string
		constraint string
	}{
		{"trivy", 5, "trivy", "0.69.3"},            // scalar, quoted
		{"syft", 6, "syft", "1.42.3"},              // scalar, bare
		{"cargo-llvm-cov", 8, "version", "0.8.x"},  // block map → nested version: line
		{"osv-scanner", 9, "osv-scanner", "2.3.5"}, // inline flow map
		{"kubectl", 10, "kubectl", "1.34.2"},       // scalar with trailing comment
	}
	for _, tc := range cases {
		vi, vk, c := findToolEntry(lines, start, end, tc.tool)
		if vi != tc.verIdx || vk != tc.verKey || c != tc.constraint {
			t.Errorf("%s: got (%d,%q,%q), want (%d,%q,%q)", tc.tool, vi, vk, c, tc.verIdx, tc.verKey, tc.constraint)
		}
	}

	// A block with no version line → not found.
	if vi, vk, c := findToolEntry(lines, start, end, "empty"); vi != -1 || vk != "" || c != "" {
		t.Errorf("empty: got (%d,%q,%q), want (-1,'','')", vi, vk, c)
	}
	// A tool absent from the section → not found.
	if vi, _, _ := findToolEntry(lines, start, end, "flux"); vi != -1 {
		t.Errorf("flux: verIdx=%d, want -1", vi)
	}
}

func TestFindToolConstraintLine(t *testing.T) {
	lines := strings.Split(`toolchains:
  cargo-llvm-cov:
    version: "0.8.7"
  trivy:
    version: "0.69.x"
  empty:
    other: 1
`, "\n")

	// cargo-llvm-cov block (key at line 1, indent 2): the `version:` line.
	if v, k := findToolConstraintLine(lines, 1, 2, 7); v != 2 || k != "version" {
		t.Errorf("cargo-llvm-cov: verIdx=%d key=%q, want 2,version", v, k)
	}
	// trivy block (key at line 3): a wildcard version.
	if v, k := findToolConstraintLine(lines, 3, 2, 7); v != 4 || k != "version" {
		t.Errorf("trivy: verIdx=%d key=%q, want 4,version", v, k)
	}
	// A block with no version line → not found.
	if v, k := findToolConstraintLine(lines, 5, 2, 7); v != -1 || k != "" {
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
