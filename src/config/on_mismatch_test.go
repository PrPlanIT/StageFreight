package config

import (
	"strings"
	"testing"
)

// on_mismatch is declared beside the reference it governs, so it must reach the resolver
// and must never reach the section it sits in — a lint section has no such field.
func TestOnMismatchIsStrippedFromTheSection(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nlint:\n  preset: lint.yml\n  on_mismatch: retained\n",
		"lint.yml", "lint:\n  level: full\n")

	cfg, _, err := LoadWithWarnings(path)
	if err != nil {
		t.Fatalf("a config declaring on_mismatch must load: %v", err)
	}
	if cfg.Lint.Level != "full" {
		t.Errorf("preset did not compose: level = %q", cfg.Lint.Level)
	}
}

// A misspelled policy must be refused rather than silently treated as the default:
// "warn" reads like it would keep going, and would instead stop.
func TestOnMismatchRejectsAnUnknownValue(t *testing.T) {
	path := writePresetFixture(t,
		"version: 1\nlint:\n  preset: https://example.org/lint.yml\n  on_mismatch: warn\n",
		"", "")

	_, _, err := LoadWithWarnings(path)
	if err == nil {
		t.Fatal("an unknown on_mismatch must not load")
	}
	for _, want := range []string{"on_mismatch", "fail", "source", "retained"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
