package freshness

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAgedLatestNpm(t *testing.T) {
	now := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	cutoff := now.Add(-7 * 24 * time.Hour) // 2026-01-03
	doc := npmFullDoc{
		DistTags: map[string]string{"latest": "8.5.15"},
		Versions: map[string]json.RawMessage{
			"8.5.13": []byte("{}"), "8.5.14": []byte("{}"),
			"8.5.15": []byte("{}"), "9.0.0-rc.1": []byte("{}"),
		},
		Time: map[string]string{
			"created":    "2020-01-01T00:00:00.000Z",
			"8.5.13":     "2025-12-01T00:00:00.000Z", // old → eligible
			"8.5.14":     "2026-01-02T00:00:00.000Z", // before cutoff → eligible (newest aged)
			"8.5.15":     "2026-01-08T00:00:00.000Z", // within cooldown → held
			"9.0.0-rc.1": "2025-11-01T00:00:00.000Z", // prerelease → skipped even though old
		},
	}
	if got := agedLatestNpm(doc, cutoff); got != "8.5.14" {
		t.Errorf("agedLatestNpm = %q, want 8.5.14 (8.5.15 within cooldown; rc excluded)", got)
	}
}

func TestParseFlexDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"7d": 7 * 24 * time.Hour, "2w": 14 * 24 * time.Hour,
		"72h": 72 * time.Hour, "": 0, "garbage": 0,
	}
	for in, want := range cases {
		if got := parseFlexDuration(in); got != want {
			t.Errorf("parseFlexDuration(%q) = %v, want %v", in, got, want)
		}
	}
}
