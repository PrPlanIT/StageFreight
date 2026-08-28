package governance

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ForgeClient abstracts forge API for file commits.
// Extends ForgeReader with write capability.
type ForgeClient interface {
	ForgeReader

	// CommitFiles commits multiple file changes to a repo's default branch.
	// Returns commit SHA on success.
	CommitFiles(repo, branch, message string, files []FileCommit) (string, error)

	// DefaultBranch returns the default branch name for a repo.
	DefaultBranch(repo string) (string, error)
}

// FileCommit is a single file change within a commit.
type FileCommit struct {
	Path    string
	Content []byte
	Action  string // "create", "update"
}

// CommitDistribution executes distribution plans by committing to target repos.
// Per-repo failure does NOT stop the run. Aggregates results.
// Idempotent: skips repos where all files are unchanged.
// Returns error if ANY repo failed.
// source is the host-qualified control-repo location that governs these plans, e.g.
// "gitlab.prplanit.com/PrPlanIT/MaintenancePolicy".
func CommitDistribution(plans []DistributionPlan, forge ForgeClient, source string, dryRun bool) ([]CommitResult, error) {
	var results []CommitResult
	var anyError bool

	for _, plan := range plans {
		result := commitRepo(plan, forge, source, dryRun)
		results = append(results, result)
		if result.Error != nil {
			anyError = true
		}
	}

	if anyError {
		return results, fmt.Errorf("governance reconcile completed with errors (see individual results)")
	}
	return results, nil
}

func commitRepo(plan DistributionPlan, forge ForgeClient, source string, dryRun bool) CommitResult {
	result := CommitResult{Repo: plan.Repo}

	// Check if anything needs writing.
	if !plan.HasChanges() {
		result.Status = "skipped-identical"
		result.Message = "all files unchanged"
		return result
	}

	// Check for drift.
	for _, f := range plan.Files {
		if f.Drifted {
			result.Drifted = true
			break
		}
	}

	if dryRun {
		result.Status = "dry-run"
		result.Message = describePlan(plan)
		return result
	}

	// Get default branch.
	branch, err := forge.DefaultBranch(plan.Repo)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("getting default branch: %w", err)
		return result
	}

	// Build file commits (skip unchanged).
	var files []FileCommit
	for _, f := range plan.Files {
		if f.Action == "unchanged" {
			continue
		}
		action := "update"
		if f.Action == "create" {
			action = "create"
		}
		files = append(files, FileCommit{
			Path:    f.Path,
			Content: f.Content,
			Action:  action,
		})
	}

	if len(files) == 0 {
		result.Status = "skipped-identical"
		result.Message = "no files to commit after filtering"
		return result
	}

	// Build attributable commit message.
	message := buildCommitMessage(source)

	sha, err := forge.CommitFiles(plan.Repo, branch, message, files)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("committing to %s: %w", plan.Repo, err)
		return result
	}

	result.Status = "committed"
	result.SHA = sha
	result.Message = describePlan(plan)
	return result
}

// buildCommitMessage returns the reconcile commit message: a subject naming WHERE the
// policy came from, and nothing else.
//
// It previously also carried the source ref and the full list of written paths. Both
// are things a commit already provides — it has a SHA, and it shows the files it
// touched — and both varied per reconcile, so no two messages matched and release notes
// rendered a row per reconcile instead of one collapsed entry. Drift was listed too,
// which the diff shows anyway.
//
// The source keeps its HOST: a control repo path alone ("PrPlanIT/MaintenancePolicy")
// reads identically whether the policy came from the internal GitLab or from
// github.com, and which one governs a repo is exactly what this line exists to answer.
// It is stable across reconciles, so it costs nothing in deduplication.
//
// The trailer is Governed-By, per the origin taxonomy in config/commit.go. NOT
// Generated-By: that marks a generation with no build impact and the rendered CI skips
// it, whereas a reconcile changes the config the satellite builds under and must build,
// or the policy just distributed is never exercised. NOT Updated-By either: nothing
// keys on it behaviourally, so its only value is legibility, and a reconcile sharing the
// deps verb would make `git log --grep` unable to tell them apart.
func buildCommitMessage(source string) string {
	return fmt.Sprintf("chore: governance reconcile from %s\n\n%s\n", source, config.GovernedByTrailer)
}

// describePlan summarizes what a distribution plan will do.
func describePlan(plan DistributionPlan) string {
	var parts []string
	for _, f := range plan.Files {
		if f.Action != "unchanged" {
			label := f.Action
			if f.Drifted {
				label += " (drifted)"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", f.Path, label))
		}
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, ", ")
}
