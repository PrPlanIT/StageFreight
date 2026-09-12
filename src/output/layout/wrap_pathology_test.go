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

// Frame integrity: no returned segment may contain a control character, so a
// caller prefixing each segment with a frame gutter can never emit an ungutered
// continuation. Regression for carried text (e.g. a CVE description) whose own
// embedded newlines shattered the box. Covers BOTH the short-line early-return
// path and the wrapped path, and confirms ANSI escapes survive.
func TestWrapContent_NeutralizesFrameBreakingControls(t *testing.T) {
	cases := map[string]string{
		"short (early return)": "curl 8.21 → 8.22\nsecond line\twith tab",
		"long (wrapped)":       "A flaw in libcurl SASL negotiation\r\nallows an incomplete handshake\nsequence " + strings.Repeat("to be misinterpreted ", 12),
		"del + vertical tab":   "before\x7fafter\x0bmore",
	}
	for name, in := range cases {
		for _, seg := range WrapContent(in, 60) {
			if strings.ContainsFunc(seg, func(r rune) bool { return (r < 0x20 && r != 0x1b) || r == 0x7f }) {
				t.Errorf("%s: segment carries a frame-breaking control char: %q", name, seg)
			}
		}
	}

	// ANSI color escapes must pass through untouched (zero-width, handled by wrap).
	colored := "\033[31mred\033[0m normal text"
	got := WrapContent(colored, 60)
	if len(got) != 1 || got[0] != colored {
		t.Errorf("ANSI escape not preserved: got %q, want %q", got, colored)
	}
}
