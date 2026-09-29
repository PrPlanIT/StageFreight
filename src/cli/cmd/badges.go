package cmd

// Package-internal badge generation. The SVG artifacts are produced here and
// referenced by stencils (![…](…/build.svg)); `stagefreight scribe apply` and
// the CI narrate stage both call RunConfigBadges. There is no standalone badge command.
import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/PrPlanIT/StageFreight/src/badge"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
)

// buildDefaultBadgeEngine creates a badge engine with the default font (dejavu-sans 11pt).
// Per-item font overrides are handled in buildItemEngine.
func buildDefaultBadgeEngine() (*badge.Engine, error) {
	return badge.NewDefault()
}

// badgeRow holds display data for a single badge in section output.
type badgeRow struct {
	Name    string
	Out     string
	Font    string
	Size    float64
	Color   string
	Changed bool
}

// RunConfigBadges generates SVG badges from scribe config items. Called by
// `stagefreight scribe apply` and the CI narrate stage.
func RunConfigBadges(appCfg *config.Config, rootDir string, names []string, status string) error {
	eng, err := buildDefaultBadgeEngine()
	if err != nil {
		return err
	}
	return generateConfigBadgesImpl(eng, appCfg, rootDir, names, status)
}

// hasConfiguredBadges reports whether any scribe badge items (inline content with an
// output path) are declared. A project without badges (e.g. a static site) SKIPS badge
// generation rather than failing — scribe apply and the narrate stage gate on this.
func hasConfiguredBadges(appCfg *config.Config) bool {
	return len(postbuild.CollectScribeBadgeItems(appCfg)) > 0
}

func generateConfigBadgesImpl(eng *badge.Engine, appCfg *config.Config, rootDir string, names []string, status string) error {
	start := time.Now()

	// All badge content defs (stencil entries that generate an SVG), in
	// document order.
	items := postbuild.CollectScribeBadgeItems(appCfg)

	if len(items) == 0 {
		return fmt.Errorf("no badge stencils configured")
	}

	// Filter to named items if specified
	if len(names) > 0 {
		nameSet := make(map[string]bool, len(names))
		for _, n := range names {
			nameSet[n] = true
		}
		var filtered []config.StencilDef
		for _, item := range items {
			// Match by badge text (label) or ID
			if nameSet[item.LabelOrID()] || (item.ID != "" && nameSet[item.ID]) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("no matching badge items for: %v", names)
		}
		items = filtered
	}

	// Detect version for template resolution
	versionInfo, err := build.DetectVersionLenient(rootDir, appCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: version detection failed: %v\n", err)
	}

	// Resolve every badge's templated Value through the shared fact pipeline
	// (gitver leaf pass → registry → inventory, in dependency order).
	specs := make([]config.BadgeSpec, len(items))
	for i, item := range items {
		specs[i] = item.ToBadgeSpec()
	}
	resolvedValues := postbuild.ResolveBadgeValues(context.Background(), specs, versionInfo, rootDir, appCfg)

	// Pass 2: resolve docker templates and generate SVGs
	var rows []badgeRow
	var written, held []string
	updated, unchanged := 0, 0

	for i, item := range items {
		spec := specs[i]

		// Resolve per-item engine if font is overridden.
		itemEng := eng
		if spec.Font != "" || spec.FontFile != "" || spec.FontSize != 0 {
			override, err := buildItemEngine(spec)
			if err != nil {
				return fmt.Errorf("loading font for badge %s: %w", item.ID, err)
			}
			itemEng = override
		}

		svg, badgeColor := postbuild.RenderBadgeSVG(itemEng, spec, resolvedValues[i], status)

		if err := os.MkdirAll(filepath.Dir(spec.Output), 0o755); err != nil {
			return fmt.Errorf("creating badge directory for %s: %w", item.LabelOrID(), err)
		}

		// A fact that could not be resolved here must not overwrite one that was
		// resolved somewhere it could be. {env:BUILD_STATUS} and friends only exist
		// inside the pipeline, so running this command anywhere else would otherwise
		// rewrite a "passing" badge to "n/a" — silently, and committed by the next
		// docs commit. Hold the existing artifact instead and say so.
		//
		// Only when one already exists: a badge that has never been generated still
		// gets its n/a, because a missing file is a broken image in every README.
		if postbuild.ValueUnresolved(spec, resolvedValues[i]) {
			if _, statErr := os.Stat(spec.Output); statErr == nil {
				written = append(written, spec.Output)
				held = append(held, spec.Output)
				continue
			}
		}
		// Write only when the content actually changed — an unchanged badge must
		// not rewrite the file, or it churns a no-op commit. (Reproducible output,
		// from zeroed font timestamps, is what makes this comparison meaningful.)
		changed := true
		if prev, rerr := os.ReadFile(spec.Output); rerr == nil && bytes.Equal(prev, []byte(svg)) {
			changed = false
		}
		if changed {
			if err := os.WriteFile(spec.Output, []byte(svg), 0o644); err != nil {
				return fmt.Errorf("writing badge %s: %w", item.ID, err)
			}
			updated++
		} else {
			unchanged++
		}

		// Collect row for section output
		fontName := spec.Font
		if fontName == "" {
			fontName = "dejavu-sans"
		}
		size := spec.FontSize
		if size == 0 {
			size = 11
		}
		written = append(written, spec.Output)
		rows = append(rows, badgeRow{
			Name:    item.LabelOrID(),
			Out:     spec.Output,
			Font:    fontName,
			Size:    size,
			Color:   badgeColor,
			Changed: changed,
		})
	}

	// Remove generated badges no stencil claims any more. A filtered run (--names)
	// only ever writes a subset, so pruning there would delete every badge it was
	// not asked about.
	var pruned []string
	if len(names) == 0 {
		var pErr error
		if pruned, pErr = pruneOrphanBadges(written); pErr != nil {
			fmt.Fprintf(os.Stderr, "  warning: pruning orphaned badges: %v\n", pErr)
		}
	}

	// Sort rows for stable output
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Out < rows[j].Out
	})

	elapsed := time.Since(start)
	useColor := output.UseColor()
	w := os.Stdout

	sec := output.NewSection(w, "Badges", elapsed, useColor)
	for _, r := range rows {
		state := output.StatusIcon("skipped", useColor) // ⊘ unchanged
		if r.Changed {
			state = output.StatusIcon("success", useColor) // ✓ updated
		}
		sec.Row("%s %-16s%-24s %-8s %.0fpt  %s", state, r.Name, r.Out, r.Font, r.Size, r.Color)
	}
	for _, p := range held {
		sec.Row("%s %-16s%-24s unresolved here — kept the existing badge",
			output.StatusIcon("skipped", useColor), "held", p)
	}
	for _, p := range pruned {
		sec.Row("%s %-16s%-24s orphaned — no stencil declares it",
			output.StatusIcon("warning", useColor), "pruned", p)
	}
	sec.Separator()
	if len(pruned) > 0 {
		sec.Row("%d updated, %d unchanged, %d pruned", updated, unchanged, len(pruned))
	} else {
		sec.Row("%d updated, %d unchanged", updated, unchanged)
	}
	sec.Close()

	return nil
}

// pruneOrphanBadges deletes generated .svg files that sit in a directory scribe just
// wrote to but that no current stencil produced.
//
// Badge output is otherwise append-only: removing a stencil stops the file being
// regenerated but never removes it, so the artifact freezes at whatever it last
// rendered and outlives the config that made it. That is how nine repos ended up
// serving an `updated` badge reading "n/a" months after the stencil was replaced by
// release-updated/dev-updated — no pipeline could have fixed it, because nothing
// claimed the file.
//
// Deliberately narrow. Only directories this run wrote into are considered, only
// .svg files are removed, and an empty written set prunes nothing — a failed or
// filtered run must never be read as "no badges belong here".
func pruneOrphanBadges(written []string) ([]string, error) {
	if len(written) == 0 {
		return nil, nil
	}
	keep := make(map[string]bool, len(written))
	dirs := make(map[string]bool)
	for _, w := range written {
		abs, err := filepath.Abs(w)
		if err != nil {
			return nil, err
		}
		keep[abs] = true
		dirs[filepath.Dir(abs)] = true
	}

	var pruned []string
	for dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return pruned, err
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".svg" {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if keep[full] {
				continue
			}
			if err := os.Remove(full); err != nil {
				return pruned, err
			}
			pruned = append(pruned, full)
		}
	}
	sort.Strings(pruned)
	return pruned, nil
}

// buildItemEngine creates a badge engine for a BadgeSpec with font overrides.
// Falls back to defaults (dejavu-sans 11pt) for any field not set.
func buildItemEngine(spec config.BadgeSpec) (*badge.Engine, error) {
	return badge.NewForSpec(spec.Font, spec.FontSize, spec.FontFile)
}
