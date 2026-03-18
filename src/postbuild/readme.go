package postbuild

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

// ReadmeHook syncs README to docker-readme targets.
func ReadmeHook() pipeline.PostBuildHook {
	return pipeline.PostBuildHook{
		Name: "readme",
		Condition: func(pc *pipeline.PipelineContext) bool {
			targets := pipeline.CollectTargetsByKind(pc.Config, "docker-readme")
			return len(targets) > 0 && !pc.Local
		},
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			targets := pipeline.CollectTargetsByKind(pc.Config, "docker-readme")
			summary, _ := RunReadmeSection(pc.Ctx, pc.Writer, pc.CI, pc.Color, targets, pc.RootDir, pc.Config)
			return &pipeline.PhaseResult{
				Name:    "readme",
				Status:  "success",
				Summary: summary,
			}, nil
		},
	}
}

// RunReadmeSection syncs README to docker-readme targets with section-formatted output.
// Returns a summary string and elapsed time for the summary table.
func RunReadmeSection(ctx context.Context, w io.Writer, _ bool, color bool, targets []config.TargetConfig, rootDir string, appCfg *config.Config) (string, time.Duration) {
	output.SectionStartCollapsed(w, "sf_readme", "README Sync")
	start := time.Now()

	var synced, errors int

	for _, t := range targets {
		// Resolve {var:...} templates in target fields
		resolvedPath := gitver.ResolveVars(t.Path, appCfg.Vars)
		resolvedDesc := gitver.ResolveVars(t.Description, appCfg.Vars)
		resolvedLinkBase := gitver.ResolveVars(t.LinkBase, appCfg.Vars)

		file := t.File
		if file == "" {
			file = "README.md"
		}

		content, err := registry.PrepareReadmeFromFile(file, resolvedDesc, resolvedLinkBase, rootDir)
		if err != nil {
			errors++
			continue
		}

		provider := t.Provider
		if provider == "" {
			provider = build.DetectProvider(t.URL)
		}

		client, err := registry.NewRegistry(provider, t.URL, t.Credentials)
		if err != nil {
			errors++
			continue
		}

		short := content.Short
		if resolvedDesc != "" {
			short = resolvedDesc
		}

		if err := client.UpdateDescription(ctx, resolvedPath, short, content.Full); err != nil {
			errors++
			continue
		}
		synced++
	}

	elapsed := time.Since(start)
	sec := output.NewSection(w, "Readme", elapsed, color)
	for _, t := range targets {
		resolvedPath := gitver.ResolveVars(t.Path, appCfg.Vars)
		sec.Row("%-40ssynced", t.URL+"/"+resolvedPath)
	}
	sec.Close()
	output.SectionEnd(w, "sf_readme")

	summary := fmt.Sprintf("%d synced", synced)
	if errors > 0 {
		summary += fmt.Sprintf(", %d error(s)", errors)
	}
	return summary, elapsed
}

// FirstDockerReadmeDescription returns the description from the first docker-readme target.
func FirstDockerReadmeDescription(cfg *config.Config) string {
	for _, t := range cfg.Targets {
		if t.Kind == "docker-readme" && t.Description != "" {
			return t.Description
		}
	}
	return ""
}
