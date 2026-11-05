package config

import (
	"strings"
	"testing"
)

// A placeholder the binary does not know would render literally into the version.
func TestBranchBuildFormatPlaceholders(t *testing.T) {
	for format, wantErr := range map[string]string{
		"{next}-dev.{commits}+{sha}": "",
		"{base}-{branch}+{sha}":      "",
		"{next}-dev.{count}+{sha}":   "placeholder {count} is unknown",
	} {
		cfg := defaults()
		cfg.Git.Tags = OrderedTagSources{{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`}}
		cfg.Git.Versioning.BranchBuilds = OrderedBranchBuilds{{ID: "default", BaseFrom: []string{"stable"}, Format: format}}
		_, err := Validate(cfg)
		switch {
		case wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", format, err)
		case wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)):
			t.Errorf("%s: err = %v, want %q", format, err, wantErr)
		}
	}
}

func TestVersioningMajors(t *testing.T) {
	for majors, ok := range map[string]bool{"": true, "always": true, "after_v1": true, "never": true, "sometimes": false} {
		cfg := defaults()
		cfg.Git.Versioning.Majors = majors
		_, err := Validate(cfg)
		if ok == (err != nil && strings.Contains(err.Error(), "versioning.majors")) {
			t.Errorf("majors %q: err = %v", majors, err)
		}
	}
}
