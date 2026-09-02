package security

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Advisory text is dense with dots that do not end sentences. Breaking on any of them
// produces something worse than a mid-word cut: a version severed at "2.1." reads as
// corrupted data rather than as shortened prose.
func TestClip_DoesNotBreakInsideVersionsPathsOrAbbreviations(t *testing.T) {
	for _, s := range []string{
		"a flaw affects all releases prior to 2.1.4 and permits remote escalation of privilege",
		"the module golang.org/x/net mishandles header parsing under concurrent load conditions",
		"input is not validated, e.g. the length field, allowing an attacker to overflow a buffer",
	} {
		got := clip(s, 48)
		body := strings.TrimSuffix(got, "...")
		for _, bad := range []string{"2.1.", "golang.", "e.g."} {
			if strings.HasSuffix(body, bad) {
				t.Errorf("broke on a non-sentence dot (%q):\n  %q", bad, got)
			}
		}
	}
}

// A sentence end is preferred when taking it costs little — here it falls past half the
// budget, so ending cleanly is nearly free. The dot after a version still ends a sentence
// when a capital follows it.
func TestClip_PrefersANearbySentenceEnd(t *testing.T) {
	s := "Remote attackers can crash the service. Additional detail follows in the advisory."
	got := clip(s, 60)
	if !strings.HasPrefix(got, "Remote attackers can crash the service") {
		t.Errorf("did not break at the sentence end: %q", got)
	}
	if strings.Contains(got, "Additional") {
		t.Errorf("kept past the sentence end: %q", got)
	}
}

// An early sentence end must NOT cost the rest: rounding back that far trades readable
// text for tidiness, and a clause that trails off still carries information. Only
// fragments that say nothing were the objection.
func TestClip_KeepsTheRemainderWhenTheSentenceEndIsEarly(t *testing.T) {
	s := "Fixed in 2.1.4. Earlier builds remain exposed to the same crafted request."
	got := clip(s, 60)
	if !strings.Contains(got, "Earlier builds") {
		t.Errorf("discarded informative text to end on an early sentence: %q", got)
	}
}

// A run-on with no sentence end must not collapse to a stub: it degrades to a word break.
func TestClip_RunOnDegradesToAWordBreak(t *testing.T) {
	s := "an attacker who is able to reach the endpoint may be able to cause the service to consume memory without bound and eventually terminate"
	got := clip(s, 60)
	if len(got) < 40 {
		t.Errorf("collapsed to a stub: %q", got)
	}
	body := strings.TrimSuffix(got, "...")
	last := body[strings.LastIndex(body, " ")+1:]
	if !strings.Contains(s, last+" ") {
		t.Errorf("ended mid-word: %q", got)
	}
}

// Non-ASCII must survive at every length: a byte slice lands mid-rune and emits a
// replacement character exactly at the cut.
func TestClip_NeverBreaksARune(t *testing.T) {
	s := "permite a un atacante — mediante una petición manipulada — escalar privilegios en el sistema."
	for max := 8; max < len([]rune(s)); max++ {
		got := clip(s, max)
		if !utf8.ValidString(got) {
			t.Fatalf("invalid UTF-8 at max=%d: %q", max, got)
		}
	}
}

// Text that fits is returned untouched — no ellipsis on something never shortened.
func TestClip_LeavesShortTextAlone(t *testing.T) {
	s := "a short description"
	if got := clip(s, 100); got != s {
		t.Errorf("modified text that fit: %q", got)
	}
}
