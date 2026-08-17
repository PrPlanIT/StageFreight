package sync

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// immutableTagPlaceholders identify a version/sha IDENTITY in a tag template — a tag that
// names one specific build (v{version}, dev-{sha:8}). A template carrying one resolves to
// an immutable tag and must never be force-overwritten on the mirror, even if it is
// declared among a target's aliases.
var immutableTagPlaceholders = []string{"{version}", "{major}", "{minor}", "{patch}", "{sha"}

// RollingAliasTagSet returns the concrete tag names that are ROLLING aliases — mutable
// pointers declared in kind:release targets' aliases that advance each release (e.g.
// "latest", "latest-dev"). Classification is FROM CONFIG, not hardcoded names: an alias
// whose template carries a version/sha placeholder is an immutable identity and is
// EXCLUDED, so the mirror force-updates rolling aliases without ever forcing a version
// tag. Concrete names come from gitver.ResolveTags; with no version info available only
// literal (placeholder-free) templates are admitted, which the rolling ones already are.
func RollingAliasTagSet(cfg *config.Config, vi *gitver.VersionInfo) map[string]bool {
	set := map[string]bool{}
	if cfg == nil {
		return set
	}
	var rolling []string
	for _, t := range cfg.Targets {
		if t.Kind != "release" {
			continue
		}
		for _, tmpl := range t.Aliases {
			if IsImmutableTagTemplate(tmpl) {
				continue
			}
			rolling = append(rolling, tmpl)
		}
	}
	if len(rolling) == 0 {
		return set
	}
	if vi == nil {
		for _, tmpl := range rolling {
			if !strings.Contains(tmpl, "{") {
				set[tmpl] = true
			}
		}
		return set
	}
	for _, name := range gitver.ResolveTags(rolling, vi) {
		if name != "" {
			set[name] = true
		}
	}
	return set
}

// IsImmutableTagTemplate reports whether a tag template names a version/sha identity —
// the marker that it is immutable: outside the rolling-alias force set on the mirror, and
// never delete-and-recreated by the alias-tagging path.
func IsImmutableTagTemplate(tmpl string) bool {
	for _, p := range immutableTagPlaceholders {
		if strings.Contains(tmpl, p) {
			return true
		}
	}
	return false
}
