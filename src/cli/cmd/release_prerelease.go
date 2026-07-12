package cmd

import (
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

// resolveMirrorPrerelease determines whether a release being projected to a mirror
// forge is a prerelease.
//
// The primary forge (notably GitLab) exposes no native prerelease field, so its
// ListReleases reports Prerelease=false for everything. Rather than lose that
// semantic on the way to a mirror (which is how dev/rolling releases ended up
// masquerading as GitHub's "latest release"), we recover the truth from:
//
//  1. the config release channel that owns the tag — the authoritative source: a
//     kind: release target declared `prerelease: true` whose tag/aliases templates
//     match the tag; and
//  2. the release-notes body, which StageFreight stamps "**Release type:** prerelease"
//     (see src/release/notes.go releaseType) — a resilient second signal for releases
//     created outside a matching target.
//
// Either signal is sufficient; a stable release matches neither.
func resolveMirrorPrerelease(cfg *config.Config, tagName, body string) bool {
	return tagMatchesPrereleaseChannel(cfg, tagName) || bodyMarksPrerelease(body)
}

// tagMatchesPrereleaseChannel reports whether tagName belongs to a kind: release
// target declared prerelease: true, matched via that target's tag/aliases templates
// (e.g. `dev-{sha:8}` → ^dev-.+$, `latest-dev`).
func tagMatchesPrereleaseChannel(cfg *config.Config, tagName string) bool {
	if cfg == nil || tagName == "" {
		return false
	}
	for _, t := range cfg.Targets {
		if t.Kind != "release" || !t.Prerelease {
			continue
		}
		var templates []string
		if t.Tag != "" {
			templates = append(templates, t.Tag)
		}
		templates = append(templates, t.Aliases...)
		if config.MatchPatterns(retention.TemplatesToPatterns(templates), tagName) {
			return true
		}
	}
	return false
}

// bodyMarksPrerelease parses the release-notes marker StageFreight writes for
// prereleases (see src/release/notes.go: "**Release type:** prerelease").
func bodyMarksPrerelease(body string) bool {
	return strings.Contains(strings.ToLower(body), "release type:** prerelease")
}
