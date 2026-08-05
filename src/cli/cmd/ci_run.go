package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/spf13/cobra"
)

var ciRunTag string

var ciRunCmd = &cobra.Command{
	Use:   "run <subsystem>",
	Short: "Run a CI subsystem",
	Long: fmt.Sprintf(`Run a CI phase or legacy subsystem by name.

Canonical phases: %s

Generated CI files set SF_CI_* environment variables, then call this command.
Phase behavior is configured in .stagefreight.yml.

Exit codes: 0=success, 1=phase error, 2=config error, 3=context error`, strings.Join(ci.ValidSubsystems(), ", ")),
	Args: cobra.ExactArgs(1),
	RunE: runCIRun,
}

func init() {
	ciRunCmd.Flags().StringVar(&ciRunTag, "tag", "", "release tag (overrides SF_CI_TAG for release subsystem)")

	ciCmd.AddCommand(ciRunCmd)
}

func runCIRun(cmd *cobra.Command, args []string) error {
	subsystem := args[0]
	ctx := context.Background()

	// Resolve CI context from SF_CI_* env vars (with git fallbacks)
	ciCtx := ci.ResolveContext()

	// Loop-prevention backstop. The rendered CI rules (GitLab workflow:rules, Actions
	// per-job gate) already decline to start a pipeline for a StageFreight-generated
	// commit, but they read a forge variable — which on Azure is subject-only and cannot
	// see a body trailer — and no rule exists for a local run. This guard reads the FULL
	// HEAD message and self-skips uniformly, so a narrate commit never re-triggers a phase
	// on any forge or locally. Tags and deps (Updated-By) commits fall through and build.
	if generatedCommitShouldSkip(ciCtx.IsTag(), headCommitMessage(resolveWorkspace(ciCtx))) {
		fmt.Printf("  %s: skipping — HEAD is a StageFreight-generated commit (%s); regenerating would only re-emit it\n", subsystem, config.GeneratedByTrailer)
		return nil
	}

	opts := ci.RunOptions{
		Tag:     ciRunTag,
		Verbose: verbose,
	}

	registry := buildCIRegistry()

	start := time.Now()
	runErr := ci.RunSubsystem(registry, subsystem, ctx, cfg, ciCtx, opts)

	// The one output allowed outside the subsystem boxes: a closing summary
	// covering everything THIS job recorded. When it renders, a failure has
	// already been shown in its row — exit silently instead of trailing the
	// boxes with a naked error string.
	summarized := renderJobSummary(os.Stdout, time.Since(start))
	if runErr != nil {
		if summarized {
			return &SilentExitError{Code: 1}
		}
		return runErr
	}

	return nil
}

// renderJobSummary renders one outcome row per subsystem recorded by this
// process (the cistate session view — the state FILE unions other jobs'
// records via forwarded artifacts, which would re-report other phases' work).
// Returns whether anything rendered.
func renderJobSummary(w *os.File, elapsed time.Duration) bool {
	subs := cistate.SessionSubsystems()
	if len(subs) == 0 {
		return false
	}
	color := output.UseColor()
	sec := output.NewSection(w, "Summary", elapsed, color)
	for _, s := range subs {
		status := s.Outcome
		suffix := ""
		if s.Outcome == "failed" && !s.Required {
			status = "warning"
			suffix = "  (advisory)"
		}
		if s.Reason != "" {
			suffix += "  " + s.Reason
		}
		output.RowStatus(sec, s.Name, suffix, status, color)
	}
	sec.Close()
	return true
}

// generatedCommitShouldSkip reports whether a phase run must self-skip because HEAD is a
// StageFreight-generated (narrate) commit. A narrate commit carries `Generated-By:
// StageFreight`; regenerating its assets would only re-emit the same commit (the loop). A
// tag always builds (explicit release intent) and a deps commit (`Updated-By`, no
// Generated-By trailer) builds, since the image must rebuild to ship the update.
func generatedCommitShouldSkip(isTag bool, headMessage string) bool {
	if isTag {
		return false
	}
	return strings.Contains(headMessage, config.GeneratedByTrailer)
}

// headCommitMessage returns the full HEAD commit message via go-git — no git binary, and
// the tip commit is present even in a shallow CI clone. Returns "" on any error, so the
// guard fails open to running the phase rather than skipping on a read failure.
func headCommitMessage(rootDir string) string {
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return ""
	}
	head, err := repo.Head()
	if err != nil {
		return ""
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		return ""
	}
	return c.Message
}
