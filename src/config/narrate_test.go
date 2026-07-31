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
