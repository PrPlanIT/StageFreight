package config

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestUnusedMatcherWarnings(t *testing.T) {
	cfg := &Config{
		Git:           GitConfig{Branches: map[string]string{"main": "^main$", "mian": "^main$", "release": "^release/", "shared": "^x$"}},
		Notifications: OrderedNotifications{{When: WhenConditions{{Branches: []string{"release"}}}}},
	}
	// "shared" came from a preset: not authored here, so never flagged.
	got := unusedMatcherWarnings(cfg, []string{"main", "mian", "release"})
	want := []string{
		"git.branches.main is defined but nothing uses it (builds, targets, notifications)",
		"git.branches.mian is defined but nothing uses it (builds, targets, notifications)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestMappingKeysReadsTheDocumentAsWritten(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("git:\n  preset: preset/git.yml\n  branches:\n    mian: ^main$\n"), &root); err != nil {
		t.Fatal(err)
	}
	if got := mappingKeys(&root, "git", "branches"); !slices.Equal(got, []string{"mian"}) {
		t.Errorf("got %v", got)
	}
	if got := mappingKeys(&root, "scribe", "files"); got != nil {
		t.Errorf("absent path: got %v", got)
	}
}
