package freshness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
)

// npmRegistryResponse matches the npm registry abbreviated response.
type npmRegistryResponse struct {
	Version string `json:"version"`
}

// checkNpm parses package.json and resolves latest versions via registry.npmjs.org.
func (m *freshnessModule) checkNpm(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	if !m.cfg.sourceEnabled(EcosystemNpm) {
		return nil, nil
	}

	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, fmt.Errorf("freshness: parse package.json: %w", err)
	}

	lines := buildLineIndex(data)

	var deps []Dependency
	for name, version := range pkg.Dependencies {
		ver := stripNpmRange(version)
		if ver == "" {
			continue
		}
		deps = append(deps, Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: EcosystemNpm,
			File:      file.Path,
			Line:      findLineForJSON(lines, name),
		})
	}

	for name, version := range pkg.DevDependencies {
		ver := stripNpmRange(version)
		if ver == "" {
			continue
		}
		deps = append(deps, Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: EcosystemNpm,
			File:      file.Path,
			Line:      findLineForJSON(lines, name),
		})
	}

	// Resolve latest versions
	for i := range deps {
		m.resolveNpmPackage(ctx, &deps[i])
	}

	return deps, nil
}

// stripNpmRange removes semver range prefixes (^, ~, >=, etc.).
func stripNpmRange(ver string) string {
	ver = strings.TrimSpace(ver)
	// Skip workspace, file, git, and URL references
	for _, prefix := range []string{"workspace:", "file:", "git:", "git+", "http:", "https:", "link:"} {
		if strings.HasPrefix(ver, prefix) {
			return ""
		}
	}
	// Remove range operators
	for _, prefix := range []string{"^", "~", ">=", ">", "<=", "<", "="} {
		if strings.HasPrefix(ver, prefix) {
			ver = strings.TrimPrefix(ver, prefix)
			break
		}
	}
	// Handle "x" ranges like "1.x" or "1.2.x"
	ver = strings.TrimRight(ver, ".x*")
	return strings.TrimSpace(ver)
}

// resolveNpmPackage queries the npm registry (or custom registry) for the latest version.
func (m *freshnessModule) resolveNpmPackage(ctx context.Context, dep *Dependency) {
	ep := m.cfg.registryEndpoint(EcosystemNpm)
	baseURL := m.cfg.registryURL(EcosystemNpm, "https://registry.npmjs.org")
	url := fmt.Sprintf("%s/%s/latest", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url

	var resp npmRegistryResponse
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return
	}
	if resp.Version != "" {
		dep.Latest = resp.Version
	}
}

// findLineForJSON finds the approximate line number for a JSON key.
func findLineForJSON(lines []string, key string) int {
	target := `"` + key + `"`
	for i, line := range lines {
		if strings.Contains(line, target) {
			return i + 1
		}
	}
	return 0
}
