package cmd

// Package-internal badge generation. The SVG artifacts are produced here and
// referenced by scribe content (![…](…/build.svg)); `stagefreight scribe apply` and
// the CI narrate stage both call RunConfigBadges. There is no standalone badge command.
import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/badge"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
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
	Name  string
	Out   string
	Font  string
	Size  float64
	Color string
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

	// All badge content defs (scribe.content entries that generate an SVG), in
	// document order.
	items := postbuild.CollectScribeBadgeItems(appCfg)

	if len(items) == 0 {
		return fmt.Errorf("no badge items configured (scribe.content badges)")
	}

	// Filter to named items if specified
	if len(names) > 0 {
		nameSet := make(map[string]bool, len(names))
		for _, n := range names {
			nameSet[n] = true
		}
		var filtered []config.ContentDef
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
	versionInfo, err := build.DetectVersion(rootDir, appCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  warning: version detection failed: %v\n", err)
	}

	// Inject project description from docker-readme targets
	if desc := postbuild.FirstDockerReadmeDescription(appCfg); desc != "" {
		gitver.SetProjectDescription(desc)
	}

	// Pass 1: resolve version templates for all badges, collect resolved values
	specs := make([]config.BadgeSpec, len(items))
	resolvedValues := make([]string, len(items))
	for i, item := range items {
		specs[i] = item.ToBadgeSpec()
		value := specs[i].Value
		if versionInfo != nil && value != "" {
			value = gitver.ResolveTemplateWithDirAndVars(value, versionInfo, rootDir, appCfg.Vars)
		}
		resolvedValues[i] = value
	}

	// Scan resolved values for {docker.tag.*} patterns to discover tag names
	tagNames := gitver.ExtractDockerTagNames(resolvedValues)

	// Lazy Docker Hub info — fetch repo-level + per-tag info if needed
	var dockerInfo *gitver.DockerHubInfo
	needsDocker := len(tagNames) > 0
	if !needsDocker {
		for _, v := range resolvedValues {
			if strings.Contains(v, "{docker.") {
				needsDocker = true
				break
			}
		}
	}
	if needsDocker {
		ns, repo := postbuild.DockerHubFromConfig(appCfg)
		if ns != "" && repo != "" {
			dockerInfo, _ = gitver.FetchDockerHubInfo(ns, repo)
			if dockerInfo != nil && len(tagNames) > 0 {
				client := &http.Client{Timeout: 10 * time.Second}
				dockerInfo.Tags = gitver.FetchTagInfo(client, ns, repo, tagNames)
			}
		}
	}

	// Pass 2: resolve docker templates and generate SVGs
	var rows []badgeRow
	generated := 0

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

		value := gitver.ResolveDockerTemplates(resolvedValues[i], dockerInfo)

		// Guard against empty or unresolved template values producing broken badges.
		// Any remaining "{" means a template didn't resolve (missing tag, nil docker info, etc).
		if value == "" || strings.Contains(value, "{") {
			value = "n/a"
		}

		// Resolve color
		badgeColor := spec.Color
		if badgeColor == "" || badgeColor == "auto" {
			badgeColor = badge.StatusColor(status)
		}

		svg := itemEng.Generate(badge.Badge{
			Label: spec.Label,
			Value: value,
			Color: badgeColor,
		})

		if err := os.MkdirAll(filepath.Dir(spec.Output), 0o755); err != nil {
			return fmt.Errorf("creating badge directory for %s: %w", item.LabelOrID(), err)
		}
		if err := os.WriteFile(spec.Output, []byte(svg), 0o644); err != nil {
			return fmt.Errorf("writing badge %s: %w", item.ID, err)
		}
		generated++

		// Collect row for section output
		fontName := spec.Font
		if fontName == "" {
			fontName = "dejavu-sans"
		}
		size := spec.FontSize
		if size == 0 {
			size = 11
		}
		rows = append(rows, badgeRow{
			Name:  item.LabelOrID(),
			Out:   spec.Output,
			Font:  fontName,
			Size:  size,
			Color: badgeColor,
		})
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
		sec.Row("%-16s%-24s %-8s %.0fpt  %s", r.Name, r.Out, r.Font, r.Size, r.Color)
	}
	sec.Separator()
	sec.Row("%d generated", generated)
	sec.Close()

	return nil
}

// buildItemEngine creates a badge engine for a BadgeSpec with font overrides.
// Falls back to defaults (dejavu-sans 11pt) for any field not set.
func buildItemEngine(spec config.BadgeSpec) (*badge.Engine, error) {
	return badge.NewForSpec(spec.Font, spec.FontSize, spec.FontFile)
}
