package cmd

import (
	"strings"
	"testing"
)

// TestTrimBodyToLength pins the max_length contract: trim at a line boundary,
// always preserving the pipeline-link line so tap-through survives the cut.
func TestTrimBodyToLength(t *testing.T) {
	body := "title line\nrow one\nrow two\nrow three\n\n→ https://forge/pipelines/1"

	// No cap, or under cap → untouched.
	if got := trimBodyToLength(body, 0); got != body {
		t.Errorf("no cap should not trim: %q", got)
	}
	if got := trimBodyToLength(body, 4096); got != body {
		t.Errorf("under cap should not trim: %q", got)
	}

	got := trimBodyToLength(body, 50)
	if len(got) > 50 {
		t.Errorf("trimmed body exceeds cap: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "→ https://forge/pipelines/1") {
		t.Errorf("pipeline link must survive the cut: %q", got)
	}
	if strings.Contains(got, "row three") {
		t.Errorf("expected mid rows to be cut first: %q", got)
	}
	if !strings.HasPrefix(got, "title line") {
		t.Errorf("leading lines kept in order: %q", got)
	}

	// No link line → plain line-boundary trim.
	plain := trimBodyToLength("aaaa\nbbbb\ncccc", 9)
	if plain != "aaaa\nbbbb" && plain != "aaaa" {
		t.Errorf("plain trim at line boundary: %q", plain)
	}
	if len(plain) > 9 {
		t.Errorf("plain trim exceeds cap: %q", plain)
	}
}

// TestOutcomeWord maps pipeline status to the when.outcomes vocabulary.
func TestOutcomeWord(t *testing.T) {
	cases := map[string]string{"passing": "success", "failing": "failure", "warning": "warning", "unknown": "unknown", "": "unknown"}
	for in, want := range cases {
		if got := outcomeWord(in); got != want {
			t.Errorf("outcomeWord(%q) = %q, want %q", in, got, want)
		}
	}
}
