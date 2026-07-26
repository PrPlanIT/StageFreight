package gitver

import (
	"testing"
	"time"
)

func TestFriendlyLayout(t *testing.T) {
	cases := map[string]string{
		"YYYY-MM-DD": "2006-01-02",
		"MM/DD/YY":   "01/02/06",
		"YYYY/MM/DD": "2006/01/02",
		"HH:mm:ss":   "15:04:05",
		"2006-01-02": "2006-01-02", // Go layout passes through untouched
		"Jan 2, 2006": "Jan 2, 2006",
	}
	for in, want := range cases {
		if got := friendlyLayout(in); got != want {
			t.Errorf("friendlyLayout(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatFriendly(t *testing.T) {
	t.Setenv("TZ", "UTC")
	tm := time.Date(2026, 7, 26, 13, 5, 9, 0, time.UTC)
	if got := formatFriendly(tm, "YYYY-MM-DD"); got != "2026-07-26" {
		t.Errorf("YYYY-MM-DD = %q", got)
	}
	if got := formatFriendly(tm, "MM/DD/YY"); got != "07/26/26" {
		t.Errorf("MM/DD/YY = %q", got)
	}
}

func TestTemplateEscape(t *testing.T) {
	v := &VersionInfo{SHA: "abc1234567"}
	if got := ResolveTemplate("dev-{{sha}}", v); got != "dev-{sha}" {
		t.Errorf("escaped {{sha}} = %q, want dev-{sha}", got)
	}
	if got := ResolveTemplate("dev-{sha}", v); got != "dev-abc1234" {
		t.Errorf("unescaped {sha} = %q, want dev-abc1234", got)
	}
}
