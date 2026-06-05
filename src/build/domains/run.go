package domains

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/runner"
	"github.com/PrPlanIT/StageFreight/src/version"
)

// Run executes a build run as a single domain-ordered narrative. Identity,
// Executor, Lint and Summary are run-level — rendered once by the run. Each
// contributor domain (Detect → Plan → Build → Verify → Publish) is rendered
// exactly once; participating contributors supply rows. The run owns the single
// Summary and the single manifest pair.
//
// The same entry serves perform (binary + crucible contributors both apply) and
// the standalone build commands (only the relevant contributor applies) — there
// is no separate "two pipeline" path anywhere.
func Run(rc *RunContext) error {
	if rc.Writer == nil {
		rc.Writer = os.Stdout
	}
	start := time.Now()

	// ── Identity (run-level, once) ──────────────────────────────
	output.Banner(rc.Writer, output.NewBannerInfo(version.Version, version.Commit, ""), rc.Color)
	output.ContextBlock(rc.Writer, pipeline.CIContextKV(), rc.Color)

	// Gather the active contributors first — the Executor strictness and every
	// domain are driven by who actually runs, not by raw config.
	active := applicable(rc)

	// ── Executor (run-level, once; strictness from active contributors) ──
	dockerRequired, isCrucible := false, false
	for _, c := range active {
		if s, ok := c.(SubstrateNeeds); ok {
			if s.NeedsDocker() {
				dockerRequired = true
			}
			if s.NeedsCrucible() {
				isCrucible = true
			}
		}
	}
	if r := pipeline.ExecutorPreflightWithWriter(rc.Writer, rc.RootDir,
		runner.Options{DockerRequired: dockerRequired, IsCrucible: isCrucible}, rc.Color); r.Health == runner.Unhealthy {
		return fmt.Errorf("perform: substrate unhealthy")
	}

	var results []pipeline.PhaseResult

	// ── Lint (run-level, once) ──────────────────────────────────
	if !rc.SkipLint {
		pc := &pipeline.PipelineContext{
			Ctx: rc.Ctx, RootDir: rc.RootDir, Config: rc.Config,
			Writer: rc.Writer, Color: rc.Color, Verbose: rc.Verbose,
		}
		lintRes, lintErr := pipeline.LintPhase().Run(pc)
		if lintRes != nil {
			results = append(results, *lintRes)
		}
		if lintErr != nil {
			pipeline.RenderRunSummary(rc.Writer, rc.Color, rc.RootDir, results, time.Since(start))
			return fmt.Errorf("perform lint gate: %w", lintErr)
		}
	}

	// ── Contributor domains ─────────────────────────────────────
	for _, d := range orderedDomains {
		res, rendered, err := runDomain(rc, d, active)
		if rendered {
			results = append(results, res)
		}
		if err != nil {
			pipeline.RenderRunSummary(rc.Writer, rc.Color, rc.RootDir, results, time.Since(start))
			return err
		}
	}

	// ── Summary (run-level, once) ───────────────────────────────
	pipeline.RenderRunSummary(rc.Writer, rc.Color, rc.RootDir, results, time.Since(start))

	// ── Single manifest pair (run owns truth; fixes the clobber) ──
	if err := finalizeManifests(rc); err != nil {
		return err
	}
	return nil
}

// runDomain renders ONE box for a domain, gathering rows from every contributor
// that participates. Contributors that don't implement the domain interface, or
// that Skip, contribute nothing. Multiple participants are separated within the
// single box. Returns one aggregated PhaseResult for the run Summary.
func runDomain(rc *RunContext, d Domain, active []Contributor) (pipeline.PhaseResult, bool, error) {
	start := time.Now()

	type part struct {
		name string
		c    Contribution
	}
	var parts []part
	var domErr error
	var errName string

	for _, c := range active {
		ctr, participates, err := callDomain(rc, d, c)
		if !participates {
			continue
		}
		if err != nil {
			domErr = err
			errName = c.Name()
			parts = append(parts, part{c.Name(), ctr}) // include partial rows for context
			break
		}
		if ctr.Skip {
			continue
		}
		parts = append(parts, part{c.Name(), ctr})
	}

	if len(parts) == 0 {
		return pipeline.PhaseResult{}, false, domErr
	}

	sec := output.NewSection(rc.Writer, d.title(), time.Since(start), rc.Color)
	status := "success"
	var summaries []string
	for i, p := range parts {
		if i > 0 {
			sec.Separator()
		}
		for _, row := range p.c.Rows {
			sec.Row("%s", row)
		}
		if p.c.Status == "failed" {
			status = "failed"
		}
		if p.c.Summary != "" {
			summaries = append(summaries, p.c.Summary)
		}
	}
	sec.Close()

	res := pipeline.PhaseResult{Name: string(d), Status: status, Summary: strings.Join(summaries, "; ")}
	if domErr != nil {
		res.Status = "failed"
		return res, true, fmt.Errorf("%s domain (%s): %w", d, errName, domErr)
	}
	return res, true, nil
}

// callDomain dispatches a contributor to a domain via its per-domain interface.
// Returns participates=false when the contributor does not implement the domain.
func callDomain(rc *RunContext, d Domain, c Contributor) (Contribution, bool, error) {
	switch d {
	case DomainDetect:
		if x, ok := c.(Detector); ok {
			ct, err := x.Detect(rc)
			return ct, true, err
		}
	case DomainPlan:
		if x, ok := c.(Planner); ok {
			ct, err := x.Plan(rc)
			return ct, true, err
		}
	case DomainBuild:
		if x, ok := c.(Builder); ok {
			ct, err := x.Build(rc)
			return ct, true, err
		}
	case DomainVerify:
		if x, ok := c.(Verifier); ok {
			ct, err := x.Verify(rc)
			return ct, true, err
		}
	case DomainPublish:
		if x, ok := c.(Publisher); ok {
			ct, err := x.Publish(rc)
			return ct, true, err
		}
	}
	return Contribution{}, false, nil
}

// finalizeManifests writes the single outputs.json/published.json pair for the
// whole run. It runs once, after every contributor has recorded into the shared
// rc.Outputs/rc.RB — so binary and docker artifacts coexist in one manifest
// instead of one pipeline clobbering the other's. Mirrors the write/build/write
// trio in docker/publish.go (outputs must be finalized before rb.Build).
func finalizeManifests(rc *RunContext) error {
	if rc.Outputs == nil || len(rc.Outputs.Artifacts) == 0 {
		return nil
	}
	if err := rc.Outputs.Finalize(); err != nil {
		return fmt.Errorf("finalizing outputs manifest: %w", err)
	}
	if err := artifact.WriteOutputsManifest(rc.RootDir, *rc.Outputs); err != nil {
		return fmt.Errorf("writing outputs manifest: %w", err)
	}
	results, err := rc.RB.Build(rc.Outputs)
	if err != nil {
		return fmt.Errorf("building results manifest: %w", err)
	}
	if err := artifact.WriteResultsManifest(rc.RootDir, results); err != nil {
		return fmt.Errorf("writing results manifest: %w", err)
	}
	return nil
}
