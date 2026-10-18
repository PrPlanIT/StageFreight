package cmd

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

func opsString(ops []scheduleOp) string {
	var out []string
	for _, op := range ops {
		key := op.Desired.Key
		if op.Kind == "delete" {
			key, _ = scheduleKey(op.Existing.Description)
			if key == "" {
				key = op.Existing.Description
			}
		}
		out = append(out, op.Kind+":"+key)
	}
	return strings.Join(out, " ")
}

func TestDesiredSchedules_GroupsAndDescribes(t *testing.T) {
	cfg := &config.Config{Schedules: config.OrderedSchedules{
		{ID: "nightly", Cron: "17 4 * * *", Reason: "upstream watch"},
		{ID: "deps", Cron: "17 4 * * *"},
		{ID: "weekly", Cron: "42 6 * * 1", Reason: "base image refresh"},
	}}
	got := desiredSchedules(cfg)
	if len(got) != 2 || got[0].Key != "nightly,deps" || got[0].Description != "stagefreight: nightly,deps — upstream watch" {
		t.Fatalf("desired = %+v", got)
	}
	if key, ok := scheduleKey(got[1].Description); !ok || key != "weekly" {
		t.Fatalf("the description must carry the ids back: %q", got[1].Description)
	}
}

func TestPlanSchedules(t *testing.T) {
	desired := []desiredSchedule{
		{Key: "nightly", Cron: "17 4 * * *", Description: "stagefreight: nightly"},
		{Key: "weekly", Cron: "42 6 * * 1", Description: "stagefreight: weekly"},
		{Key: "fresh", Cron: "0 5 * * *", Description: "stagefreight: fresh"},
	}
	inSync := forge.PipelineSchedule{ID: 1, Description: "stagefreight: nightly", Ref: "main", Cron: "17 4 * * *", Timezone: "UTC", Active: true, OwnerID: 7}
	existing := []forge.PipelineSchedule{
		inSync,
		{ID: 2, Description: "stagefreight: weekly", Ref: "main", Cron: "0 6 * * 1", Timezone: "UTC", Active: false, OwnerID: 9}, // drifted, inactive, foreign owner
		{ID: 3, Description: "stagefreight: retired", Ref: "main", Cron: "0 1 * * *", Timezone: "UTC", Active: true, OwnerID: 7},
		{ID: 4, Description: "stagefreight: nightly", Ref: "main", Cron: "17 4 * * *", Timezone: "UTC", Active: true, OwnerID: 7}, // duplicate
		{ID: 5, Description: "a human's own schedule", Ref: "main", Cron: "0 0 * * *", Active: true, OwnerID: 2},
	}
	got := opsString(planSchedules(desired, existing, "main", 7))
	for _, want := range []string{"update:weekly", "own:weekly", "create:fresh", "delete:retired", "delete:nightly"} {
		if !strings.Contains(got, want) {
			t.Errorf("ops %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "update:nightly") || strings.Contains(got, "human") {
		t.Errorf("ops %q touched an in-sync or foreign schedule", got)
	}
	if strings.Count(got, "delete:nightly") != 1 {
		t.Errorf("only the duplicate nightly is pruned: %q", got)
	}

	if got := opsString(planSchedules(desired[:1], []forge.PipelineSchedule{inSync}, "main", 0)); got != "" {
		t.Errorf("an unknown token user must not trigger ownership changes: %q", got)
	}
	if got := opsString(planSchedules(nil, existing[2:3], "main", 7)); got != "delete:retired" {
		t.Errorf("removing every schedule prunes the owned ones: %q", got)
	}
}
