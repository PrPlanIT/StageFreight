package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrderedTargetsMapForm: the publish: map decodes in document order with each
// target's ID stamped from its key.
func TestOrderedTargetsMapForm(t *testing.T) {
	y := "b-target: { kind: registry, build: x }\n" +
		"a-target: { kind: release }\n" +
		"c-target: { kind: pages }\n"
	var ot OrderedTargets
	if err := yaml.Unmarshal([]byte(y), &ot); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ot) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(ot))
	}
	wantIDs := []string{"b-target", "a-target", "c-target"} // document order, NOT sorted
	for i, want := range wantIDs {
		if ot[i].ID != want {
			t.Fatalf("target[%d].ID = %q, want %q (order/id from key)", i, ot[i].ID, want)
		}
	}
	if ot[0].Kind != "registry" || ot[1].Kind != "release" || ot[2].Kind != "pages" {
		t.Fatalf("kinds not decoded: %+v", ot)
	}
}

// TestOrderedTargetsRejectsList: the retired list form is not accepted by the
// publish grammar (map-only).
func TestOrderedTargetsRejectsList(t *testing.T) {
	var ot OrderedTargets
	if err := yaml.Unmarshal([]byte("- id: x\n  kind: registry\n"), &ot); err == nil {
		t.Fatal("publish must reject the list form")
	}
}

// TestPublishHardBreaksTargets: the retired targets: key no longer parses — a
// config using it fails at decode (KnownFields), the hard break.
func TestPublishHardBreaksTargets(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".stagefreight.yml")
	if err := os.WriteFile(p, []byte("version: 1\ntargets:\n  - id: x\n    kind: registry\n    build: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadWithWarnings(p); err == nil {
		t.Fatal("expected load to fail on the retired targets: key (hard break)")
	}
}
