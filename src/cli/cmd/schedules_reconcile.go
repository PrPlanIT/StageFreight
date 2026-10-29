package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// scheduleMarker prefixes the description of every forge schedule StageFreight owns. Only
// marked schedules are converged or pruned; anyone else's are never touched.
const scheduleMarker = "stagefreight: "

// scheduleVar is the variable a forge schedule sets so the run knows which ids fired it.
const scheduleVar = "SF_CI_SCHEDULE"

// desiredSchedule is one cadence StageFreight wants on the forge.
type desiredSchedule struct {
	Key         string // the ids, comma-separated: the schedule's identity on the forge
	Cron        string
	Description string
}

// scheduleOp is one change the reconcile makes.
type scheduleOp struct {
	Kind     string // create | update | own | delete
	Desired  desiredSchedule
	Existing forge.PipelineSchedule
}

// desiredSchedules builds the forge-side state the config declares.
func desiredSchedules(cfg *config.Config) []desiredSchedule {
	reasons := map[string]string{}
	for _, s := range cfg.Schedules {
		reasons[s.ID] = s.Reason
	}
	var out []desiredSchedule
	for _, r := range config.ResolvedSchedules(cfg) {
		key := strings.Join(r.IDs, ",")
		var why []string
		for _, id := range r.IDs {
			if reasons[id] != "" {
				why = append(why, reasons[id])
			}
		}
		desc := scheduleMarker + key
		if len(why) > 0 {
			desc += " — " + strings.Join(why, "; ")
		}
		out = append(out, desiredSchedule{Key: key, Cron: r.Cron, Description: desc})
	}
	return out
}

// scheduleKey returns the ids an owned forge schedule carries, from its description.
func scheduleKey(description string) (string, bool) {
	rest, ok := strings.CutPrefix(description, scheduleMarker)
	if !ok {
		return "", false
	}
	key, _, _ := strings.Cut(rest, " — ")
	return strings.TrimSpace(key), true
}

// ownedSchedules indexes the schedules StageFreight owns by ids. A second schedule carrying
// the same ids is a duplicate, returned separately for pruning.
func ownedSchedules(existing []forge.PipelineSchedule) (owned map[string]forge.PipelineSchedule, dups []forge.PipelineSchedule) {
	owned = map[string]forge.PipelineSchedule{}
	for _, e := range existing {
		key, ok := scheduleKey(e.Description)
		if !ok {
			continue
		}
		if _, dup := owned[key]; dup {
			dups = append(dups, e)
			continue
		}
		owned[key] = e
	}
	return owned, dups
}

// planSchedules diffs desired against the forge's schedules. Pure: the caller applies the ops.
// An owned schedule is identified by its ids, so changing a cron updates it in place.
func planSchedules(desired []desiredSchedule, existing []forge.PipelineSchedule, branch string, me int) []scheduleOp {
	owned, dups := ownedSchedules(existing)
	var ops []scheduleOp
	for _, e := range dups {
		ops = append(ops, scheduleOp{Kind: "delete", Existing: e})
	}
	want := map[string]bool{}
	for _, d := range desired {
		want[d.Key] = true
		e, ok := owned[d.Key]
		if !ok {
			ops = append(ops, scheduleOp{Kind: "create", Desired: d})
			continue
		}
		if e.Cron != d.Cron || e.Ref != branch || e.Timezone != "UTC" || e.Description != d.Description || !e.Active {
			ops = append(ops, scheduleOp{Kind: "update", Desired: d, Existing: e})
		}
		if me != 0 && e.OwnerID != me {
			ops = append(ops, scheduleOp{Kind: "own", Desired: d, Existing: e})
		}
	}
	for key, e := range owned {
		if !want[key] {
			ops = append(ops, scheduleOp{Kind: "delete", Existing: e})
		}
	}
	return ops
}

// reconcileSchedules converges the forge's pipeline schedules to schedules:. It runs only
// where schedules are forge API state (GitLab) and only on the default branch (accepted
// state). It never fails the run: the forge is an availability provider, so a token that
// cannot manage schedules degrades to a warning and the schedule-inactive signal.
func reconcileSchedules(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) {
	fc, err := newForgeClient(forge.Provider(ciCtx.Provider), ciCtx.RepoURL)
	if err != nil {
		return
	}
	sched, ok := fc.(forge.PipelineScheduler)
	if !ok {
		return // schedules are rendered into this forge's CI file
	}
	desired := desiredSchedules(appCfg)
	sub := cistate.SubsystemState{
		Name: "schedules", Attempted: true, Completed: true, AllowFailure: true,
		Outcome: "success", Reason: "schedules converged",
		Results: map[string]string{"managed": strconv.Itoa(len(desired))},
	}
	fail := func(err error) {
		fmt.Printf("  schedules: %v\n", err)
		sub.Outcome, sub.Reason = "warning", err.Error()
		if len(desired) > 0 {
			sub.Results[cistate.SignalsKey] = config.SignalScheduleInactive
		}
		recordSubsystemOutcome(rootDir, sub)
	}

	existing, err := sched.ListPipelineSchedules(ctx)
	if err != nil {
		if len(desired) == 0 {
			return // nothing declared and nothing visible: not this run's concern
		}
		fail(fmt.Errorf("cannot manage pipeline schedules (the forge token needs the Maintainer role): %w", err))
		return
	}
	me, _ := sched.CurrentUserID(ctx)

	var changes, inactive []string
	varChecked := map[string]bool{}
	for _, e := range existing {
		if key, owned := scheduleKey(e.Description); owned && !e.Active {
			inactive = append(inactive, key)
		}
	}
	for _, op := range planSchedules(desired, existing, ciCtx.DefaultBranch, me) {
		want := forge.PipelineSchedule{
			ID: op.Existing.ID, Description: op.Desired.Description, Ref: ciCtx.DefaultBranch,
			Cron: op.Desired.Cron, Timezone: "UTC", Active: true,
		}
		var err error
		id := op.Existing.ID
		switch op.Kind {
		case "create":
			id, err = sched.CreatePipelineSchedule(ctx, want)
		case "update":
			err = sched.UpdatePipelineSchedule(ctx, want)
		case "own":
			err = sched.TakePipelineScheduleOwnership(ctx, id)
		case "delete":
			err = sched.DeletePipelineSchedule(ctx, id)
		}
		if err == nil && (op.Kind == "create" || op.Kind == "update") {
			err = ensureScheduleVar(ctx, sched, id, op.Desired.Key, op.Kind == "create")
			varChecked[op.Desired.Key] = true
		}
		if err != nil {
			fail(err)
			return
		}
		label := op.Desired.Key
		if op.Kind == "delete" {
			label, _ = scheduleKey(op.Existing.Description)
		}
		changes = append(changes, op.Kind+" "+label)
	}
	// Variables of schedules that needed no other change are still checked: a hand-edited
	// variable would silently mis-route the run.
	owned, _ := ownedSchedules(existing)
	for _, d := range desired {
		if e, ok := owned[d.Key]; ok && !varChecked[d.Key] {
			if err := ensureScheduleVar(ctx, sched, e.ID, d.Key, false); err != nil {
				fail(err)
				return
			}
		}
	}

	if len(desired) == 0 && len(changes) == 0 && len(inactive) == 0 {
		// Nothing declared, nothing owned, nothing to do: stay silent instead of emitting a
		// "0 in sync" line and a telemetry row on every publish across the whole fleet.
		return
	}
	if len(changes) == 0 {
		fmt.Printf("  schedules: %d in sync\n", len(desired))
	} else {
		fmt.Printf("  schedules: %s\n", strings.Join(changes, ", "))
		sub.Results["changes"] = strings.Join(changes, ", ")
	}
	if len(inactive) > 0 {
		// Found deactivated (its owner lost access, or someone paused it) and reactivated.
		fmt.Printf("  schedules: reactivated %s\n", strings.Join(inactive, "; "))
		sub.Results[cistate.SignalsKey] = config.SignalScheduleInactive
	}
	recordSubsystemOutcome(rootDir, sub)
}

// ensureScheduleVar makes the schedule's SF_CI_SCHEDULE variable name its ids.
func ensureScheduleVar(ctx context.Context, sched forge.PipelineScheduler, id int, key string, fresh bool) error {
	if fresh {
		return sched.SetPipelineScheduleVariable(ctx, id, scheduleVar, key, false)
	}
	s, err := sched.GetPipelineSchedule(ctx, id)
	if err != nil {
		return err
	}
	v, exists := s.Variables[scheduleVar]
	if exists && v == key {
		return nil
	}
	return sched.SetPipelineScheduleVariable(ctx, id, scheduleVar, key, exists)
}
