package layout

import (
	"strings"
	"testing"
)

// A huge, near-spaceless line with a false "value column" (an early ≥2-space gap
// followed by a long unbreakable token — the shape of a whole RUN command dumped
// into an error box) must NOT explode into hundreds of one-char rows. Regression
// for the char-per-line wrapping blowup.
func TestWrapContent_GiantLowWhitespaceLineBounded(t *testing.T) {
	line := strings.Repeat("a", 100) + "  " + strings.Repeat("x", 1500)
	out := WrapContent(line, 120)

	if len(out) > 20 {
		t.Fatalf("wrapped into %d lines; must be bounded to ≤ 20", len(out))
	}
	// No non-final line may be a sliver (the per-char failure mode): each carries
	// real content, not just indent + 1 char + "...".
	for i, l := range out[:len(out)-1] {
		content := strings.TrimRight(strings.TrimLeft(l, " "), ".…")
		if VisualWidth(content) < 20 {
			t.Errorf("line %d is a sliver (%q) — indent starved the cut budget", i, l)
		}
	}
}

// The indent clamp: when aligning to the value column would leave < half the width,
// the hang is abandoned so continuation lines keep a usable budget.
func TestWrapContent_AbandonsStarvingIndent(t *testing.T) {
	line := strings.Repeat("k", 90) + "  " + strings.Repeat("v", 400) // indent ~92 > 60
	out := WrapContent(line, 120)
	for i, l := range out[1:] {
		if strings.HasPrefix(l, strings.Repeat(" ", 60)) {
			t.Errorf("continuation line %d kept a starving indent: %q", i+1, l)
		}
	}
}

// A tiny budget must not panic or spin (floor + line cap protect it).
func TestWrapContent_TinyBudgetBounded(t *testing.T) {
	out := WrapContent(strings.Repeat("z", 500), 2)
	if len(out) == 0 || len(out) > 20 {
		t.Fatalf("tiny-budget wrap produced %d lines; want 1..20", len(out))
	}
}
