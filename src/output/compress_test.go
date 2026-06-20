package output

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

func TestCompressLineRanges(t *testing.T) {
	mk := func(lines ...int) []lint.Finding {
		g := make([]lint.Finding, 0, len(lines))
		for _, l := range lines {
			g = append(g, lint.Finding{Line: l})
		}
		return g
	}
	cases := []struct {
		name     string
		findings []lint.Finding
		want     string
		wantMore int
	}{
		{"singles", mk(14, 18, 25), "14, 18, 25", 0},
		{"a-run", mk(36, 37, 38, 39), "36-39", 0},
		{"mixed", mk(14, 18, 25, 36, 37, 38, 39, 42), "14, 18, 25, 36-39, 42", 0},
		{"dedupe-same-line", mk(7, 7, 7), "7", 0},
		{"unsorted", mk(42, 14, 39, 36, 37, 38), "14, 36-39, 42", 0},
		{"no-positive-lines", mk(0, 0), "", 0},
	}
	for _, c := range cases {
		got, more := compressLineRanges(c.findings)
		if got != c.want || more != c.wantMore {
			t.Errorf("%s: got (%q,%d), want (%q,%d)", c.name, got, more, c.want, c.wantMore)
		}
	}
}

func TestCompressLineRanges_Caps(t *testing.T) {
	// 20 non-adjacent lines → 20 tokens, capped at 12 with 8 counted as "more".
	g := make([]lint.Finding, 0, 20)
	for i := 0; i < 20; i++ {
		g = append(g, lint.Finding{Line: i*2 + 1}) // 1,3,5,... (no adjacency)
	}
	got, more := compressLineRanges(g)
	if more != 8 {
		t.Errorf("more = %d, want 8 (got display %q)", more, got)
	}
}
