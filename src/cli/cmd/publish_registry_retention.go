package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// registryRetentionJob is a resolved remote-registry retention task.
type registryRetentionJob struct {
	provider    string
	url         string
	path        string
	credentials string
	label       string
	tagPatterns []string // candidate tag templates (from target.Tags)
	policy      config.RetentionPolicy
}

// planRegistryRetention selects REMOTE kind:registry targets with active retention
// that match the current event, and builds their retention jobs. Local registries
// are excluded — their retention is local-daemon hygiene owned by perform. The
// concrete tags produced this run are added to each policy's protect set so the
// tags just pushed are never pruned. Pure (no network) for testability.
func planRegistryRetention(appCfg *config.Config, vi *gitver.VersionInfo) []registryRetentionJob {
	var jobs []registryRetentionJob
	for _, t := range pipeline.CollectTargetsByKind(appCfg, "registry") {
		if t.Retention == nil || !t.Retention.Active() {
			continue
		}
		if !config.TargetMatchesEnv(t, appCfg) {
			continue
		}
		resolved, err := config.ResolveRegistryForTarget(t, appCfg.Registries, appCfg.Vars)
		if err != nil || resolved.Provider == "local" {
			continue // local-daemon retention is perform's job
		}
		policy := *t.Retention
		policy.Protect = append(append([]string{}, policy.Protect...), gitver.ResolveTags(t.Tags, vi)...)
		jobs = append(jobs, registryRetentionJob{
			provider:    resolved.Provider,
			url:         resolved.URL,
			path:        resolved.Path,
			credentials: resolved.Credentials,
			label:       resolved.URL + "/" + resolved.Path,
			tagPatterns: t.Tags,
			policy:      policy,
		})
	}
	return jobs
}

// pruneRemoteRegistries applies retention to remote registry tags AFTER promotion
// has pushed this run's tags. Publish is the only phase permitted to mutate
// external distribution targets, and only here is the final remote tag set
// (existing + just-pushed) known — so this is the only correct place for it.
// No-op when no remote registry target with retention matches the event.
func pruneRemoteRegistries(ctx context.Context, appCfg *config.Config, rootDir string, w io.Writer) error {
	vi, err := build.DetectVersion(rootDir, appCfg)
	if err != nil {
		return fmt.Errorf("registry retention: detecting version: %w", err)
	}
	jobs := planRegistryRetention(appCfg, vi)
	if len(jobs) == 0 {
		return nil
	}

	color := output.UseColor()
	output.SectionStart(w, "sf_registry_retention", "Retention")
	start := time.Now()

	type jobResult struct {
		label   string
		kept    int
		deleted []string
		hint    string  // aggregated plain-English remediation for scope-denied deletes
		others  []error // non-403 delete errors, surfaced individually
	}
	var results []jobResult
	for _, j := range jobs {
		client, cerr := registry.NewRegistry(j.provider, j.url, j.credentials)
		if cerr != nil {
			fmt.Fprintf(w, "  ERROR: %s: %v\n", j.label, cerr)
			continue
		}
		res, rerr := registry.ApplyRetention(ctx, client, j.path, j.tagPatterns, j.policy)
		if rerr != nil {
			fmt.Fprintf(w, "  ERROR: %s: %v\n", j.label, rerr)
			continue
		}
		// Aggregate per-tag delete failures by root cause. A 403 is credential-wide,
		// not per-tag, so N identical scope rejections collapse into one remediation
		// line instead of a wall; genuinely distinct (non-403) errors are surfaced
		// individually.
		var forbidden int
		var others []error
		for _, e := range res.Errors {
			if registry.IsForbidden(e) {
				forbidden++
			} else {
				others = append(others, e)
			}
		}
		hint := ""
		if forbidden > 0 {
			// forbidden = tags actually attempted; res.Blocked = the tags the
			// short-circuit spared from a doomed retry — same cause, counted together.
			hint = registry.ScopeDeniedMessage(j.provider, j.credentials, forbidden+len(res.Blocked))
		}
		results = append(results, jobResult{
			label: j.label, kept: res.Kept, deleted: res.Deleted, hint: hint, others: others,
		})
	}

	sec := output.NewSection(w, "Retention", time.Since(start), color)
	for _, r := range results {
		// Two-space separator so an over-40-char registry path (which %-40s can't pad)
		// never butts up against "kept".
		sec.Row("%-40s  kept %d, pruned %d", r.label, r.kept, len(r.deleted))
		for _, d := range r.deleted {
			sec.Row("  - %s", d)
		}
		if r.hint != "" {
			sec.Row("  ! %s", r.hint)
		}
		for _, e := range r.others {
			sec.Row("  ERROR: %v", e)
		}
	}
	sec.Close()
	output.SectionEnd(w, "sf_registry_retention")
	return nil
}
