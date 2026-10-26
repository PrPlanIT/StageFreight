package discovery

import (
	"context"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

const (
	releasesPerPage = 100 // GitHub releases page size
	maxReleasePages = 10  // bound: up to 1000 releases scanned before giving up on a wildcard line
)

// collectMatchingReleaseTags paginates release tags (GitHub returns them NEWEST-first)
// until the constraint's line is FOUND, the last page is read, or the page cap is hit.
// Because releases are newest-first, the newest member of a line is its highest patch —
// so once selection finds a match, older pages can only hold lower versions and we stop.
// fetchPage returns the (v-stripped) tags for a 1-indexed page and whether more pages
// may follow. Bounded so a nonexistent line cannot loop forever (the caller then sees no
// match → unresolved).
func collectMatchingReleaseTags(constraint string, fetchPage func(page int) ([]string, bool, error)) []string {
	var tags []string
	for page := 1; page <= maxReleasePages; page++ {
		pageTags, more, err := fetchPage(page)
		if err != nil {
			break
		}
		tags = append(tags, pageTags...)
		if version.SelectConstraint(constraint, tags) != "" {
			break // found the line's newest (= highest) member
		}
		if !more {
			break // partial page → last page
		}
	}
	return tags
}

// checkToolchains generates Dependency entries from the toolchains config.
// Each pinned tool version is checked against its upstream GitHub release.
// This is the replacement for Dockerfile ENV scanning — versions now live in config.
func (m *Resolver) checkToolchains(ctx context.Context, desired map[string]config.ToolConstraint) []supplychain.Dependency {
	if !m.cfg.SourceEnabled(supplychain.EcosystemToolchain) {
		return nil
	}

	// The machine-maintained resolution of each wildcard constraint lives in
	// .stagefreight/toolchains.lock (read relative to cwd, the same repo-root assumption
	// under which .stagefreight.yml is read). A missing lock yields an empty one.
	lock, _ := toolchain.ReadLock("")

	var deps []supplychain.Dependency

	for _, def := range toolchain.AllTools() {
		pin, ok := desired[def.Name]
		if !ok || pin.Constraint == "" {
			continue // not materialized in desired — skip
		}

		// Constraint → CandidateSet → Selection. An EXACT constraint is its own
		// current version (no candidate set needed); a WILDCARD (1.26.x) resolves the
		// highest upstream member of its line as the current version, and records the
		// highest overall version as Latest for out-of-line notification.
		constraint := strings.TrimPrefix(strings.TrimSpace(pin.Constraint), "v")
		wildcard := version.IsWildcardConstraint(constraint)

		dep := supplychain.Dependency{
			Name:      def.Name,
			Ecosystem: supplychain.EcosystemToolchain,
			File:      ".stagefreight.yml",
			Binding:   fmt.Sprintf("toolchains.%s", def.Name),
		}
		if !wildcard {
			dep.Current = constraint
		}

		if wildcard {
			dep.Constraint = constraint // the line; the cooldown steps back within it
		}

		switch def.ReleaseSourceKind() {
		case "github":
			ep := m.cfg.Registries.GitHub
			baseURL := m.cfg.registryURL(supplychain.EcosystemGitHubRelease, "https://api.github.com")
			if wildcard {
				// Candidate set: the release list (PAGINATED — a wildcard's line may be
				// older than the 100 most-recent releases). Selection: highest matching member.
				dep.SourceURL = fmt.Sprintf("%s/repos/%s/%s/releases", strings.TrimRight(baseURL, "/"), def.GitHubOwner, def.GitHubRepo)
				tags := collectMatchingReleaseTags(constraint, func(page int) ([]string, bool, error) {
					return m.fetchReleasePage(ctx, &dep, def.GitHubOwner, def.GitHubRepo, page)
				})
				dep.Current = version.SelectConstraint(constraint, tags) // resolved line member
			} else {
				url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(baseURL, "/"), def.GitHubOwner, def.GitHubRepo)
				dep.SourceURL = url
				var release githubRelease
				if err := m.http.fetchJSON(ctx, url, &release, ep); err == nil && release.TagName != "" {
					dep.Latest = versionFromTag(release.TagName)
					setReleased(&dep, dep.Latest, release.PublishedAt)
				}
				if !m.cooldownFor(dep).IsZero() {
					_, _, _ = m.fetchReleasePage(ctx, &dep, def.GitHubOwner, def.GitHubRepo, 1) // dates the releases to step back to
				}
			}
		case "k8s":
			// dl.k8s.io exposes per-line channels: stable-<major>.<minor>.txt selects the
			// newest patch of a line; stable.txt is the newest overall.
			if wildcard {
				channel := kubectlChannel(constraint)
				dep.SourceURL = "https://dl.k8s.io/release/" + channel + ".txt"
				if sel, err := m.fetchKubectlChannel(ctx, channel); err == nil {
					dep.Current = sel
				}
			} else {
				dep.SourceURL = "https://dl.k8s.io/release/stable.txt"
			}
			if latest, err := m.fetchKubectlChannel(ctx, "stable"); err == nil && latest != "" {
				dep.Latest = latest
			}
			// The channels carry no dates; the project's GitHub releases do.
			if def.GitHubOwner != "" && !m.cooldownFor(dep).IsZero() {
				_, _, _ = m.fetchReleasePage(ctx, &dep, def.GitHubOwner, def.GitHubRepo, 1)
			}
		default:
			dep.SourceURL = "" // no upstream resolver; exact constraint stands alone
		}

		// Wildcard resolved-lock: the switch resolved the newest in-line member into
		// Current — that is the update TARGET (where the lock should move to). A LOCKED
		// wildcard uses its recorded `resolved` version as Current for CONTINUITY (it
		// stays put until an update pass deliberately moves the lock); an unlocked one
		// resolves to the newest in-line (first resolution → Current == target).
		if wildcard {
			dep.Latest = dep.Current // newest in-line = update target
			if locked := lock.Resolved(def.Name); locked != "" {
				dep.Current = locked
				if dep.Latest == "" {
					dep.Latest = locked // line vanished upstream → stay locked, not "unresolved"
				}
			} else if dep.Latest != "" {
				// Resolvable wildcard with no lock entry yet: Current already equals the
				// target, but the lock must still be born. Flag it so the filter routes it
				// to apply (which writes the first lock) instead of skipping "up to date".
				dep.LockPending = true
			}
		}

		// A wildcard that matched nothing upstream AND has no lock is unresolved — the
		// declared line does not exist. Never render that as up-to-date.
		if wildcard && dep.Current == "" {
			dep.ResolutionError = fmt.Sprintf("no released version matches constraint %q", pin.Constraint)
		}

		deps = append(deps, dep)
	}

	return deps
}

// kubectlChannel maps a wildcard constraint to a dl.k8s.io channel: "1.26.x" →
// "stable-1.26"; a major-or-broader wildcard falls back to "stable".
func kubectlChannel(constraint string) string {
	segs := strings.Split(constraint, ".")
	if len(segs) >= 2 && segs[0] != "x" && segs[0] != "X" && segs[1] != "x" && segs[1] != "X" {
		return fmt.Sprintf("stable-%s.%s", segs[0], segs[1])
	}
	return "stable"
}

// fetchKubectlChannel fetches a dl.k8s.io release channel (e.g. "stable",
// "stable-1.26") and returns the version it names.
func (m *Resolver) fetchKubectlChannel(ctx context.Context, channel string) (string, error) {
	body, err := m.http.fetchBytes(ctx, "https://dl.k8s.io/release/"+channel+".txt")
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %w", channel, err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(body)), "v"), nil
}
