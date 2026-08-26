// Package postbuild contains post-build hook adapters that coordinate between
// the pipeline framework and external system packages (registry, badge, etc.).
// These are integration glue — they belong to neither the pure system packages
// nor the generic pipeline framework.
package postbuild

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/badge"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// BadgeRunner generates badges and returns a summary + elapsed time.
type BadgeRunner func(w io.Writer, color bool, rootDir string) (string, time.Duration)

// BadgeHook generates configured badges.
// Condition: returns true only if scribe config has badge items.
// runner is required and non-nil by contract — every caller has a legitimate badge runner.
func BadgeHook(appCfg *config.Config, runner BadgeRunner) pipeline.PostBuildHook {
	return pipeline.PostBuildHook{
		Name: "badges",
		Condition: func(pc *pipeline.PipelineContext) bool {
			for _, c := range appCfg.Stencils {
				if c.HasGeneration() {
					return true
				}
			}
			return false
		},
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			summary, _ := runner(pc.Writer, pc.Color, pc.RootDir)
			return &pipeline.PhaseResult{
				Name:    "badges",
				Status:  "success",
				Summary: summary,
			}, nil
		},
	}
}

// RunBadgeSection generates configured badges with section-formatted output.
func RunBadgeSection(w io.Writer, color bool, rootDir string, appCfg *config.Config) (string, time.Duration) {
	output.SectionStartCollapsed(w, "sf_badges", "Badges")
	start := time.Now()

	eng, err := badge.NewDefault()
	if err != nil {
		elapsed := time.Since(start)
		sec := output.NewSection(w, "Badges", elapsed, color)
		sec.Row("error: %v", err)
		sec.Close()
		output.SectionEnd(w, "sf_badges")
		return fmt.Sprintf("error: %v", err), elapsed
	}

	items := CollectScribeBadgeItems(appCfg)

	// Detect version for template resolution
	vi, _ := build.DetectVersionLenient(rootDir, appCfg)

	// Resolve every badge's templated Value through the shared fact pipeline.
	specs := make([]config.BadgeSpec, len(items))
	for i, item := range items {
		specs[i] = item.ToBadgeSpec()
	}
	resolvedValues := ResolveBadgeValues(context.Background(), specs, vi, rootDir, appCfg)

	// Pass 2: resolve docker templates and generate SVGs
	var generated int
	for i := range items {
		spec := specs[i]

		// Per-item engine if font is overridden
		itemEng := eng
		if spec.Font != "" || spec.FontFile != "" || spec.FontSize != 0 {
			override, oErr := badge.NewForSpec(spec.Font, spec.FontSize, spec.FontFile)
			if oErr != nil {
				continue
			}
			itemEng = override
		}

		value := resolvedValues[i]

		// Guard against empty or unresolved template values producing broken badges. A
		// remaining "{" means a template didn't resolve — UNLESS the source had a {{…}}
		// literal, in which case the "{" is intentional (e.g. a "dev-{sha}" scheme).
		if value == "" || (!strings.Contains(specs[i].Value, "{{") && strings.Contains(value, "{")) {
			value = "n/a"
		}

		// Resolve color
		badgeColor := spec.Color
		if badgeColor == "" || badgeColor == "auto" {
			badgeColor = badge.StatusColor(value)
		}

		svg := itemEng.Generate(badge.Badge{
			Label: spec.Label,
			Value: value,
			Color: badgeColor,
		})

		if mkErr := os.MkdirAll(filepath.Dir(spec.Output), 0o755); mkErr != nil {
			continue
		}
		if wErr := os.WriteFile(spec.Output, []byte(svg), 0o644); wErr != nil {
			continue
		}
		generated++
	}

	elapsed := time.Since(start)
	sec := output.NewSection(w, "Badges", elapsed, color)
	for _, item := range items {
		spec := item.ToBadgeSpec()
		fontName := spec.Font
		if fontName == "" {
			fontName = "dejavu-sans"
		}
		size := spec.FontSize
		if size == 0 {
			size = 11
		}
		badgeColor := spec.Color
		if badgeColor == "" {
			badgeColor = "auto"
		}
		sec.Row("%-16s%-24s %-8s %.0fpt  %s", item.LabelOrID(), spec.Output, fontName, size, badgeColor)
	}
	sec.Close()
	output.SectionEnd(w, "sf_badges")

	summary := fmt.Sprintf("%d generated", generated)
	return summary, elapsed
}

// ResolveBadgeValues resolves each badge spec's templated Value through the full
// fact pipeline, in dependency order — the single ordered resolution path shared by
// both badge generators (the CI post-build hook RunBadgeSection and the CLI
// RunConfigBadges used by `scribe apply` / narrate). New fact families join HERE,
// not in each caller. The passes, in order:
//
//  1. gitver leaf pass, per value — version/vars/commit/project/date/… (a var value
//     may itself carry a downstream token, which the leaf pass resolves recursively).
//  2. {registry.<id>.*} — batch across all values, each registry fetched once.
//  3. {inventory.<cluster>.count} — batch, each gitops cluster discovered once.
//
// The batch passes are ordered after the leaf pass and are offline no-ops when no
// {registry.*}/{inventory.*} tokens are present.
func ResolveBadgeValues(ctx context.Context, specs []config.BadgeSpec, vi *gitver.VersionInfo, rootDir string, cfg *config.Config) []string {
	// {project.description} is config-sourced (metadata), not git-derivable — inject it
	// explicitly so both badge generators resolve it identically. (It formerly rode a
	// gitver package-global that only the CLI generator set, so the CI hook resolved it
	// from whatever ambient state happened to be present.)
	opts := gitver.ResolveOptions{ProjectDescription: FirstProjectDescription(cfg)}
	values := make([]string, len(specs))
	for i, s := range specs {
		v := s.Value
		if vi != nil && v != "" {
			v = gitver.ResolveTemplateWithOpts(v, vi, rootDir, cfg.Vars, opts)
		}
		values[i] = v
	}
	values = ResolveRegistryTemplates(ctx, values, cfg)
	values = ResolveInventoryTemplates(ctx, values, cfg, rootDir)
	return values
}

// CollectScribeBadgeItems returns all scribe content defs that generate a badge SVG.
func CollectScribeBadgeItems(appCfg *config.Config) []config.StencilDef {
	var items []config.StencilDef
	for _, c := range appCfg.Stencils {
		if c.HasGeneration() {
			items = append(items, c)
		}
	}
	return items
}
