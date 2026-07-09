package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/ci"
	"github.com/PrPlanIT/StageFreight/src/cistate"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/pages"
)

// pagesPublishRunner deploys kind: pages targets (static sites) to their provider
// (Cloudflare/GitHub Pages) during the publish phase, alongside release/package. It is
// a no-op (no card) when no pages target matches the current event. Set
// SF_PAGES_DRY_RUN=1 to stage + validate without externalizing (safe first pass).
func pagesPublishRunner(ctx context.Context, appCfg *config.Config, ciCtx *ci.CIContext, _ ci.RunOptions) error {
	rootDir := resolveWorkspace(ciCtx)

	targets := pipeline.CollectTargetsByKind(appCfg, "pages")
	if len(targets) == 0 {
		return nil
	}
	var active []config.TargetConfig
	for _, t := range targets {
		if config.TargetMatchesEnv(t, appCfg) {
			active = append(active, t)
		}
	}
	if len(active) == 0 {
		return nil
	}

	// Mutation safety: a Pages deploy moves a rolling location (mutable) — never
	// deploy from a superseded pipeline, which could roll docs backward.
	if !ci.IsBranchHeadFresh(ciCtx) {
		fmt.Fprintf(os.Stderr, "  pages: skipped — pipeline SHA is not branch HEAD\n")
		return nil
	}

	dryRun := os.Getenv("SF_PAGES_DRY_RUN") == "1"
	color := output.UseColor()
	w := os.Stdout
	deployed := 0

	for _, t := range active {
		provider, err := pages.Get(t.Provider)
		if err != nil {
			return fmt.Errorf("pages subsystem: target %s: %w", t.ID, err)
		}

		ws, cleanup, werr := pagesWorkspace(rootDir, t)
		if werr != nil {
			return fmt.Errorf("pages subsystem: target %s: %w", t.ID, werr)
		}

		project := t.Project // cloudflare project name; falls back to the target id
		if project == "" {
			project = t.ID
		}
		dopts := pages.DeployOpts{
			Project: project,     // cloudflare project name (explicit project:, else target id)
			Repo:    t.ProjectID, // github OWNER/REPO (empty → current repo from env)
			Domain:  t.Domain,
			Include: t.Include,
			Exclude: t.Exclude,
			Env:     pagesCredentials(provider),
			DryRun:  dryRun,
		}

		if err := provider.Prepare(ws, dopts); err != nil {
			cleanup()
			return fmt.Errorf("pages subsystem: target %s: prepare: %w", t.ID, err)
		}
		url, derr := provider.Deploy(ctx, ws, dopts)
		cleanup()
		if derr != nil {
			return fmt.Errorf("pages subsystem: target %s: deploy: %w", t.ID, derr)
		}

		output.SectionStart(w, "sf_pages", "Distribution (pages)")
		sec := output.NewSection(w, "Distribution (pages)", 0, color)
		sec.Row("%-12s%s", "target", t.ID)
		sec.Row("%-12s%s", "provider", t.Provider)
		sec.Separator()
		sec.Row("%s  DEPLOYED  %s", output.StatusIcon("success", color), url)
		sec.Close()
		output.SectionEnd(w, "sf_pages")
		deployed++
	}

	if deployed > 0 {
		if err := cistate.UpdateState(rootDir, func(st *cistate.State) {
			st.RecordSubsystem(cistate.SubsystemState{
				Name: "pages", Attempted: true, AllowFailure: true, Outcome: "published",
			})
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: pipeline state write failed: %v\n", err)
		}
	}
	return nil
}

// pagesWorkspace materializes a target's site content into a mutable temp workspace:
// extract a build's transport archive, or copy a committed repo dir. The caller must
// invoke cleanup.
func pagesWorkspace(rootDir string, t config.TargetConfig) (string, func(), error) {
	ws, err := os.MkdirTemp("", "sf-pages-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(ws) }

	if t.Dir != "" {
		if err := copyDirInto(filepath.Join(rootDir, t.Dir), ws); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("copying dir %q: %w", t.Dir, err)
		}
		return ws, cleanup, nil
	}

	transport, err := artifact.ResolveSuccessfulBuildOutput(rootDir, t.Build)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := transport.Extract(ws); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// The transport archive nests the tree under the artifact's basename; the site
	// root is the single top-level directory. Descend into it.
	return descendSingleDir(ws), cleanup, nil
}

// pagesCredentials reads the provider's declared credential env vars from the runner
// environment (CI secrets), for forwarding into the deploy.
func pagesCredentials(p pages.Provider) map[string]string {
	env := map[string]string{}
	for _, cr := range p.Credentials() {
		if v := os.Getenv(cr.Name); v != "" {
			env[cr.Name] = v
		}
	}
	return env
}

func descendSingleDir(ws string) string {
	entries, err := os.ReadDir(ws)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return ws
	}
	return filepath.Join(ws, entries[0].Name())
}

func copyDirInto(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
