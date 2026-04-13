package freshness

import (
	"context"
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// checkToolchainDesired generates Dependency entries from toolchains.desired config.
// Each desired tool version is checked against its upstream GitHub release.
// This is the replacement for Dockerfile ENV scanning — versions now live in config.
func (m *freshnessModule) checkToolchainDesired(ctx context.Context, desired map[string]config.ToolPinConfig) []Dependency {
	if !m.cfg.sourceEnabled(EcosystemToolchain) {
		return nil
	}

	var deps []Dependency

	for _, def := range toolchain.AllTools() {
		pin, ok := desired[def.Name]
		if !ok || pin.Version == "" {
			continue // not materialized in desired — skip
		}

		dep := Dependency{
			Name:      def.Name,
			Current:   strings.TrimPrefix(pin.Version, "v"),
			Ecosystem: EcosystemToolchain,
			File:      ".stagefreight.yml",
			Binding:   fmt.Sprintf("toolchains.desired.%s.version", def.Name),
		}

		// Check upstream for latest version
		switch {
		case def.GitHubOwner != "" && def.GitHubRepo != "":
			// GitHub releases API
			ep := m.cfg.Registries.GitHub
			baseURL := m.cfg.registryURL(EcosystemGitHubRelease, "https://api.github.com")
			url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", strings.TrimRight(baseURL, "/"), def.GitHubOwner, def.GitHubRepo)
			dep.SourceURL = url

			var release githubReleaseLatest
			if err := m.http.fetchJSON(ctx, url, &release, ep); err == nil && release.TagName != "" {
				dep.Latest = strings.TrimPrefix(release.TagName, "v")
			}
		case def.Name == "kubectl":
			// Kubernetes uses dl.k8s.io stable channel
			dep.SourceURL = "https://dl.k8s.io/release/stable.txt"
			latest, err := m.fetchKubectlStable(ctx)
			if err == nil && latest != "" {
				dep.Latest = latest
			}
		}

		deps = append(deps, dep)
	}

	return deps
}

// fetchKubectlStable fetches the latest stable kubectl version from dl.k8s.io.
func (m *freshnessModule) fetchKubectlStable(ctx context.Context) (string, error) {
	body, err := m.http.fetchBytes(ctx, "https://dl.k8s.io/release/stable.txt")
	if err != nil {
		return "", fmt.Errorf("kubectl stable: %w", err)
	}
	return strings.TrimPrefix(strings.TrimSpace(string(body)), "v"), nil
}
