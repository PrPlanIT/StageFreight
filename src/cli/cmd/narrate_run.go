package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/release"
	"github.com/PrPlanIT/StageFreight/src/scribe"
)

// runNarrate is the narrate phase's body. It records the narrate-time facts the
// earlier phases can't ({project}/{modality}/{pipeline_url}/{commit_title} identity
// plus the {changelog.*} range), renders each announced stencil as a structured-
// output card (user stencils shadow the shipped two-arc defaults), persists the
// default arc's DETERMINISTIC render as the run's "last summary", and dispatches
// notifications. Presentation never hard-fails the pipeline: every hiccup degrades
// gracefully rather than failing the phase.
func runNarrate(appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) error {
	if _, err := cistate.ReadState(rootDir); err != nil {
		fmt.Printf("  narrate: no run state to narrate (%v)\n", err)
		return nil
	}

	recordNarrateFacts(appCfg, ciCtx, rootDir)

	st, err := cistate.ReadState(rootDir)
	if err != nil {
		fmt.Printf("  narrate: run state unreadable (%v)\n", err)
		return nil
	}

	record := announceCards(appCfg, rootDir, st)
	persistLastSummary(rootDir, record)
	dispatchNotifications(appCfg, rootDir, st)
	return nil
}

// recordNarrateFacts fills the identity facts and the changelog range into cistate
// (best-effort, fill-if-empty) so every stencil render — cards, notifications,
// scribe — reads the same recorded truth.
func recordNarrateFacts(appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) {
	count, since, breaking := changelogFacts(rootDir, appCfg)
	if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
		if st.CI.Project == "" {
			st.CI.Project = detectProjectName(rootDir)
		}
		if st.CI.Modality == "" {
			st.CI.Modality = appCfg.Mode().Name
		}
		if st.CI.PipelineURL == "" {
			st.CI.PipelineURL = pipelineURL(ciCtx)
		}
		if st.CI.CommitTitle == "" {
			st.CI.CommitTitle = gitstate.HeadCommitTitle(rootDir)
		}
		if st.CI.Ref == "" {
			st.CI.Ref = firstNonEmptyStr(ciCtx.Branch, ciCtx.Tag)
		}
		if st.CI.Version == "" {
			if vi, vErr := build.DetectVersion(rootDir, appCfg); vErr == nil && vi != nil {
				st.CI.Version = vi.Version
			}
		}
		// Both or neither: a count with no baseline (no previous release tag) makes
		// "since" a claim with nothing to point at — the changelog line elides instead.
		if count > 0 && since != "" {
			facts := map[string]string{
				"count": strconv.Itoa(count),
				"range": since,
			}
			if len(breaking) > 0 {
				facts["breaking"] = strings.Join(breaking, "\n") // {changelog} producer rows
			}
			st.MergeSubsystemResults("changelog", facts)
		}
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
	}
}

// changelogFacts derives the {changelog.count}/{changelog.range} facts and the
// {changelog} producer's breaking-change rows from the SAME source release notes
// use — one changelog, rendered many ways. Any git hiccup degrades to no facts.
func changelogFacts(rootDir string, cfg *config.Config) (count int, since string, breaking []string) {
	var tagPatterns []string
	for _, ts := range cfg.Git.Tags {
		tagPatterns = append(tagPatterns, ts.Pattern)
	}
	prev, _ := release.PreviousReleaseTag(rootDir, "HEAD", tagPatterns)
	commits, err := release.ParseCommits(rootDir, prev, "HEAD")
	if err != nil {
		return 0, prev, nil
	}
	for _, c := range commits {
		if !c.Breaking {
			continue
		}
		if c.Scope != "" {
			breaking = append(breaking, c.Scope+": "+c.Summary)
		} else {
			breaking = append(breaking, c.Summary)
		}
	}
	return len(commits), prev, breaking
}

// announceCards renders each announced stencil inside a structured-output card and
// returns the first card's text (the run's persisted record). narrate.announces:
// lists stencil ids; omitted, the shipped default announces the run's ARC — the
// postmortem on a failing run, the summary otherwise (branching lives in routing,
// never in bodies). User stencils shadow the shipped defaults through the one
// resolution path. A stencil that fails to render degrades to a warning.
func announceCards(appCfg *config.Config, rootDir string, st *cistate.State) string {
	announces := appCfg.Narrate.Announces
	if len(announces) == 0 {
		if st.PipelineStatus() == "failing" {
			announces = []string{"postmortem"}
		} else {
			announces = []string{"summary"}
		}
	}
	color := output.UseColor()
	record := ""
	for _, id := range announces {
		text, err := scribe.RenderContent(appCfg, rootDir, id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: narrate announce %q: %v\n", id, err)
			continue
		}
		if record == "" {
			record = text
		}
		sec := output.NewSection(os.Stdout, announceTitle(id), 0, color)
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			sec.Row("%s", line)
		}
		sec.Close()
	}
	return record
}

// announceTitle renders a stencil id as a card header: the shipped arcs get their
// friendly names; any other id appears verbatim (config-legible — the header IS the id).
func announceTitle(id string) string {
	switch id {
	case "summary":
		return "Summary"
	case "postmortem":
		return "Postmortem"
	}
	return id
}

// persistLastSummary writes the deterministic record to the run's durable namespace
// as the "last summary." Write-if-changed (the render is reproducible, so identical
// bytes must not rewrite the file) keeps it churn-free if it's ever committed.
func persistLastSummary(rootDir, record string) {
	if record == "" {
		return
	}
	dir := filepath.Join(rootDir, paths.Root, "narrate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: narrate summary dir: %v\n", err)
		return
	}
	out := filepath.Join(dir, "summary.md")
	if prev, rerr := os.ReadFile(out); rerr == nil && bytes.Equal(prev, []byte(record)) {
		return // unchanged — don't rewrite
	}
	if err := os.WriteFile(out, []byte(record), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: narrate summary write: %v\n", err)
		return
	}
	fmt.Printf("  narrate: summary → %s\n", filepath.Join(paths.Root, "narrate", "summary.md"))
}

func detectProjectName(rootDir string) string {
	if pm := gitver.DetectProject(rootDir); pm != nil && pm.Name != "" {
		return pm.Name
	}
	return filepath.Base(rootDir)
}

// pipelineURL constructs the forge pipeline/run URL from context, best-effort. An
// empty result elides the "→ {pipeline_url}" line gracefully.
func pipelineURL(ciCtx *ci.CIContext) string {
	if ciCtx.RepoURL == "" || ciCtx.PipelineID == "" {
		return ""
	}
	base := strings.TrimSuffix(ciCtx.RepoURL, "/")
	switch ciCtx.Provider {
	case "gitlab":
		return base + "/-/pipelines/" + ciCtx.PipelineID
	case "github":
		return base + "/actions/runs/" + ciCtx.PipelineID
	default:
		return ""
	}
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
