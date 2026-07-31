package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/narrate"
	"github.com/PrPlanIT/StageFreight/src/paths"
	"github.com/PrPlanIT/StageFreight/src/release"
)

// runNarrate is the narrate phase's body: it reads the run's recorded truth
// (cistate) plus the release changelog, stamps the modality's story stencil, prints
// the story to stdout (raw markdown — the front-row seat, deliberately NOT a boxed
// section, so it travels as-is), and persists the DETERMINISTIC story as the run's
// "last summary." Presentation never hard-fails the pipeline: a changelog or write
// hiccup degrades gracefully rather than failing the phase.
func runNarrate(appCfg *config.Config, ciCtx *ci.CIContext, rootDir string) error {
	st, err := cistate.ReadState(rootDir)
	if err != nil {
		fmt.Printf("  narrate: no run state to narrate (%v)\n", err)
		return nil
	}

	in := narrate.Input{
		Project:      detectProjectName(rootDir),
		Modality:     appCfg.Mode().Name,
		Status:       st.PipelineStatus(),
		Ref:          firstNonEmptyStr(st.CI.Ref, ciCtx.Branch, ciCtx.Tag),
		SHA:          shortSHA(firstNonEmptyStr(st.CI.SHA, ciCtx.SHA)),
		Version:      detectVersionString(rootDir, appCfg),
		PipelineURL:  pipelineURL(ciCtx),
		Phases:       narratePhases(st),
		Published:    st.Build.PublishedCount,
		ReleaseTag:   releaseTagIfCut(st),
		Housekeeping: narrateHousekeeping(st),
	}
	in.Changes, in.ChangeCount, in.SinceRef = narrateChangelog(rootDir, appCfg)

	story := narrate.RenderStory(in)
	fmt.Println()
	fmt.Println(story)
	persistLastSummary(rootDir, story)
	return nil
}

// narratePhases projects the recorded subsystems (attempted only) into the story's
// phase views — Name/Outcome/Reason/Blocking, in document order.
func narratePhases(st *cistate.State) []narrate.Phase {
	var phases []narrate.Phase
	for _, s := range st.Subsystems {
		if !s.Attempted {
			continue
		}
		phases = append(phases, narrate.Phase{
			Name:     s.Name,
			Outcome:  s.Outcome,
			Reason:   s.Reason,
			Blocking: s.Blocking,
		})
	}
	return phases
}

// narrateChangelog builds the Changes section from the SAME source release notes
// use — one changelog, rendered many ways. Any git hiccup degrades to no changes.
func narrateChangelog(rootDir string, cfg *config.Config) (groups []narrate.ChangeGroup, count int, since string) {
	var tagPatterns []string
	for _, ts := range cfg.Git.Tags {
		tagPatterns = append(tagPatterns, ts.Pattern)
	}
	prev, _ := release.PreviousReleaseTag(rootDir, "HEAD", tagPatterns)
	commits, err := release.ParseCommits(rootDir, prev, "HEAD")
	if err != nil {
		return nil, 0, prev
	}
	for _, cat := range release.Categorize(commits) {
		g := narrate.ChangeGroup{Title: cat.Title}
		for _, c := range cat.Commits {
			g.Entries = append(g.Entries, narrate.ChangeEntry{
				Scope:    c.Scope,
				Summary:  c.Summary,
				Breaking: c.Breaking,
			})
		}
		groups = append(groups, g)
	}
	return groups, len(commits), prev
}

// narrateHousekeeping composes the retention/mirror footer lines from recorded state.
func narrateHousekeeping(st *cistate.State) []string {
	var hk []string
	if r := st.Retention.Local; r != nil && r.Pruned > 0 {
		hk = append(hk, fmt.Sprintf("retention — pruned %d local cache entries", r.Pruned))
	}
	if r := st.Retention.External; r != nil && r.Pruned > 0 {
		if r.Registry != "" {
			hk = append(hk, fmt.Sprintf("retention — pruned %d from `%s`", r.Pruned, r.Registry))
		} else {
			hk = append(hk, fmt.Sprintf("retention — pruned %d remote entries", r.Pruned))
		}
	}
	if m := st.GetSubsystem("mirror"); m != nil && m.Attempted {
		icon := "✓"
		if m.Outcome != "success" {
			icon = "✗"
		}
		hk = append(hk, "mirror — "+icon)
	}
	return hk
}

// releaseTagIfCut returns the release tag only when a release was actually eligible
// this run (empty otherwise → the story's release line simply drops).
func releaseTagIfCut(st *cistate.State) string {
	if st.Release.Eligible {
		return st.CI.Tag
	}
	return ""
}

// persistLastSummary writes the deterministic story to the run's durable namespace
// as the "last summary." Write-if-changed (the story is reproducible, so identical
// bytes must not rewrite the file) keeps it churn-free if it's ever committed.
func persistLastSummary(rootDir, story string) {
	dir := filepath.Join(rootDir, paths.Root, "narrate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: narrate summary dir: %v\n", err)
		return
	}
	out := filepath.Join(dir, "summary.md")
	if prev, rerr := os.ReadFile(out); rerr == nil && bytes.Equal(prev, []byte(story)) {
		return // unchanged — don't rewrite
	}
	if err := os.WriteFile(out, []byte(story), 0o644); err != nil {
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

func detectVersionString(rootDir string, cfg *config.Config) string {
	if vi, err := build.DetectVersion(rootDir, cfg); err == nil && vi != nil {
		return vi.Version
	}
	return ""
}

// pipelineURL constructs the forge pipeline/run URL from context, best-effort. An
// empty result drops the "[pipeline](…)" bit from the identity line gracefully.
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
