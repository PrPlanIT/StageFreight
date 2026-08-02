package llm

import "testing"

// TestStripThinking: reasoning models (deepseek-r1) wrap deliberation in
// <think>…</think>; the notification carries the answer only.
func TestStripThinking(t *testing.T) {
	cases := []struct{ in, want string }{
		{"<think>hmm, tests failed…</think>Start with substituteInline.", "Start with substituteInline."},
		{"plain answer", "plain answer"},
		{"a<think>x</think>b<think>y</think>c", "abc"},
		{"<think>never closed", ""}, // unclosed drops to end-of-text
	}
	for _, tc := range cases {
		if got := stripThinking(tc.in); got != tc.want {
			t.Errorf("stripThinking(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
