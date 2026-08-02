package config

import (
	"strings"
	"testing"
)

func TestScribeConfig_IsZero(t *testing.T) {
	if !(ScribeConfig{}).IsZero() {
		t.Error("empty ScribeConfig should be zero")
	}
	if (ScribeConfig{Files: OrderedFiles{{ID: "x", File: "README.md"}}}).IsZero() {
		t.Error("ScribeConfig with files is not zero")
	}
	if (ScribeConfig{Commit: ScribeCommit{Message: "m"}}).IsZero() {
		t.Error("ScribeConfig with a commit message is not zero")
	}
}

// TestOutputSpec_Worktree covers the worktree opt-in semantics: absent = pure artifact,
// true = land at source, path = land at a rename target.
func TestOutputSpec_Worktree(t *testing.T) {
	none := OutputSpec{Type: "tree", Source: "docs/modules"}
	if none.LandsInWorktree() {
		t.Error("no worktree → should not land")
	}

	yes := OutputSpec{Type: "tree", Source: "docs/modules", Worktree: &WorktreeSpec{Set: true}}
	if !yes.LandsInWorktree() || yes.WorktreePath() != "docs/modules" {
		t.Errorf("worktree: true → lands at source; got lands=%v path=%q", yes.LandsInWorktree(), yes.WorktreePath())
	}

	renamed := OutputSpec{Type: "tree", Source: "dist", Worktree: &WorktreeSpec{Set: true, Path: "docs/site"}}
	if !renamed.LandsInWorktree() || renamed.WorktreePath() != "docs/site" {
		t.Errorf("worktree: <path> → lands at path; got lands=%v path=%q", renamed.LandsInWorktree(), renamed.WorktreePath())
	}
}

// TestValidate_NarrateAnnounces covers announce id resolution: a declared stencil id
// and the built-in "summary" pass; an unknown id fails.
func TestValidate_NarrateAnnounces(t *testing.T) {
	hasAnnounceErr := func(cfg *Config) bool {
		_, err := Validate(cfg)
		return err != nil && strings.Contains(err.Error(), "narrate.announces")
	}

	builtin := &Config{Version: 1, Narrate: NarrateConfig{Announces: []string{"summary"}}}
	if hasAnnounceErr(builtin) {
		t.Error("built-in \"summary\" should be announceable")
	}

	declared := &Config{Version: 1,
		Stencils: OrderedStencils{{ID: "fun-fact", Type: "text", Body: "hi"}},
		Narrate:  NarrateConfig{Announces: []string{"fun-fact"}}}
	if hasAnnounceErr(declared) {
		t.Error("a declared stencil id should be announceable")
	}

	unknown := &Config{Version: 1, Narrate: NarrateConfig{Announces: []string{"nope"}}}
	if !hasAnnounceErr(unknown) {
		t.Error("an unknown announce id should fail validation")
	}
}

// TestValidate_StencilEmbedCycle covers cycle rejection: text stencils embedding each
// other in a loop (or a self-embed) fail validation; a linear chain passes.
func TestValidate_StencilEmbedCycle(t *testing.T) {
	hasCycleErr := func(stencils OrderedStencils) bool {
		_, err := Validate(&Config{Version: 1, Stencils: stencils})
		return err != nil && strings.Contains(err.Error(), "embed cycle")
	}

	chain := OrderedStencils{
		{ID: "a", Type: "text", Body: "a then {b}"},
		{ID: "b", Type: "text", Body: "b then {sha}"}, // {sha} is a fact, not an edge
	}
	if hasCycleErr(chain) {
		t.Error("a linear embed chain should pass validation")
	}

	loop := OrderedStencils{
		{ID: "a", Type: "text", Body: "a sees {b}"},
		{ID: "b", Type: "text", Body: "b sees {a}"},
	}
	if !hasCycleErr(loop) {
		t.Error("a two-stencil embed loop should fail validation")
	}

	self := OrderedStencils{{ID: "a", Type: "text", Body: "me: {a}"}}
	if !hasCycleErr(self) {
		t.Error("a self-embed should fail validation")
	}
}

// TestValidate_WorktreeCollision covers the safeguard that two build outputs may not land
// at the same working-tree path (which would make the tree order-dependent).
func TestValidate_WorktreeCollision(t *testing.T) {
	mk := func(id, src string) BuildConfig {
		return BuildConfig{ID: id, Kind: "command", Command: "x",
			Outputs: []OutputSpec{{Type: "tree", Source: src, Worktree: &WorktreeSpec{Set: true}}}}
	}
	cfg := &Config{Version: 1, Builds: []BuildConfig{mk("a", "docs/modules"), mk("b", "docs/modules")}}
	_, err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "worktree paths must be unique") {
		t.Errorf("colliding worktree paths should fail; got: %v", err)
	}
}
