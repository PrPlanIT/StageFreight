package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestWhenConditionsDecode: when: accepts a single condition map OR a list.
func TestWhenConditionsDecode(t *testing.T) {
	var single WhenConditions
	if err := yaml.Unmarshal([]byte("{ events: [tag] }"), &single); err != nil {
		t.Fatalf("map form: %v", err)
	}
	if len(single) != 1 || len(single[0].Events) != 1 {
		t.Fatalf("map form decoded wrong: %+v", single)
	}
	var list WhenConditions
	if err := yaml.Unmarshal([]byte("- { events: [tag] }\n- { branches: [main] }\n"), &list); err != nil {
		t.Fatalf("list form: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list form decoded wrong: %+v", list)
	}
}

// TestWhenOrList: a list of condition-sets is OR — the SF binary-archive shape,
// fires on EITHER a stable tag OR a main push, but not a feature-branch push.
func TestWhenOrList(t *testing.T) {
	tgt := TargetConfig{When: WhenConditions{
		{GitTags: []string{"stable"}, Events: []string{"tag"}},
		{Branches: []string{"main"}, Events: []string{"push"}},
	}}
	tagPol := map[string]string{"stable": `^v\d+\.\d+\.\d+$`}
	brPol := map[string]string{"main": `^main$`}

	if !TargetMatches(tgt, "tag", "", "v1.2.3", "", tagPol, brPol) {
		t.Fatal("OR-list should match on a stable tag")
	}
	if !TargetMatches(tgt, "push", "main", "", "", tagPol, brPol) {
		t.Fatal("OR-list should match on a main push")
	}
	if TargetMatches(tgt, "push", "feature", "", "", tagPol, brPol) {
		t.Fatal("OR-list should NOT match a feature-branch push")
	}
}

// TestWhenSingleAndUnconditional: single-condition precision is preserved, and an
// empty when: is unconditional.
func TestWhenSingleAndUnconditional(t *testing.T) {
	single := TargetConfig{When: WhenConditions{{Events: []string{"tag"}}}}
	if !TargetMatches(single, "tag", "", "", "", nil, nil) {
		t.Fatal("single condition should match its event")
	}
	if TargetMatches(single, "push", "main", "", "", nil, nil) {
		t.Fatal("single condition should reject a non-matching event")
	}

	uncond := TargetConfig{}
	if !TargetMatches(uncond, "push", "main", "", "", nil, nil) {
		t.Fatal("empty when should be unconditional (always eligible)")
	}
	if !TargetIsUnconditional(uncond) {
		t.Fatal("empty when should report unconditional")
	}
}
