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

// checkToolchainDesired generates Dependency entries from toolchains.desired config.
// Each desired tool version is checked against its upstream GitHub release.
// This is the replacement for Dockerfile ENV scanning — versions now live in config.
func (m *Resolver) checkToolchainDesired(ctx context.Context, desired map[string]config.ToolConstraint) []supplychain.Dependency {
	if !m.cfg.SourceEnabled(supplychain.EcosystemToolchain) {
		return nil
	}

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
			Binding:   fmt.Sprintf("toolchains.desired.%s.constraint", def.Name),
		}
		if !wildcard {
			dep.Current = constraint
		}

		switch def.ReleaseSourceKind() {
		case "github":
			ep := m.cfg.Registries.GitHub
			baseURL := m.cfg.registryURL(supplychain.EcosystemGitHubRelease, "https://api.github.com")
			if wildcard {
				// Candidate set: the release list. Selection: highest matching member.
				url := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", strings.TrimRight(baseURL, "/"), def.GitHubOwner, def.GitHubRepo)
				dep.SourceURL = url
				var rels []githubReleaseLatest
				if err := m.http.fetchJSON(ctx, url, &rels, ep); err == nil {
					tags := make([]string, 0, len(rels))
					for _, r := range rels {
						if r.TagName != "" {
							tags = append(tags, strings.TrimPrefix(r.TagName, "v"))
						}
					}
					dep.Current = version.SelectConstraint(constraint, tags) // resolved line member
					dep.Latest = version.SelectConstraint("x.x.x", tags)      // highest overall stable
				}
			} else {
				url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(baseURL, "/"), def.GitHubOwner, def.GitHubRepo)
				dep.SourceURL = url
				var release githubReleaseLatest
				if err := m.http.fetchJSON(ctx, url, &release, ep); err == nil && release.TagName != "" {
					dep.Latest = strings.TrimPrefix(release.TagName, "v")
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
		default:
			dep.SourceURL = "" // no upstream resolver; exact constraint stands alone
		}

		// A wildcard that matched nothing upstream is unresolved — the declared line
		// does not exist. Never render that as up-to-date.
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
