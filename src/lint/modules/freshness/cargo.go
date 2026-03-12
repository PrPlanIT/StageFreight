package freshness

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"

	toml "github.com/pelletier/go-toml/v2"
)

// cratesIOResponse matches the crates.io API response.
type cratesIOResponse struct {
	Crate struct {
		MaxVersion string `json:"max_version"`
	} `json:"crate"`
}

// checkCargo parses Cargo.toml and resolves latest versions via crates.io.
func (m *freshnessModule) checkCargo(ctx context.Context, file lint.FileInfo) ([]Dependency, error) {
	if !m.cfg.sourceEnabled(EcosystemCargo) {
		return nil, nil
	}

	data, err := os.ReadFile(file.AbsPath)
	if err != nil {
		return nil, err
	}

	// Parse Cargo.toml
	var cargo struct {
		Dependencies    map[string]any `toml:"dependencies"`
		DevDependencies map[string]any `toml:"dev-dependencies"`
	}
	if err := toml.Unmarshal(data, &cargo); err != nil {
		return nil, fmt.Errorf("freshness: parse Cargo.toml: %w", err)
	}

	// Convert to dependencies
	var deps []Dependency
	lines := buildLineIndex(data)

	for name, spec := range cargo.Dependencies {
		ver := extractCargoVersion(spec)
		if ver == "" {
			continue
		}
		deps = append(deps, Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: EcosystemCargo,
			File:      file.Path,
			Line:      findLineForKey(lines, name),
		})
	}

	for name, spec := range cargo.DevDependencies {
		ver := extractCargoVersion(spec)
		if ver == "" {
			continue
		}
		deps = append(deps, Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: EcosystemCargo,
			File:      file.Path,
			Line:      findLineForKey(lines, name),
		})
	}

	// Resolve latest versions
	for i := range deps {
		m.resolveCrate(ctx, &deps[i])
	}

	return deps, nil
}

// extractCargoVersion handles both "1.0" and {version = "1.0"} dependency specs.
func extractCargoVersion(spec any) string {
	switch v := spec.(type) {
	case string:
		return stripCargoRange(v)
	case map[string]any:
		if ver, ok := v["version"]; ok {
			if s, ok := ver.(string); ok {
				return stripCargoRange(s)
			}
		}
	}
	return ""
}

// stripCargoRange removes Cargo version range operators.
func stripCargoRange(ver string) string {
	ver = strings.TrimSpace(ver)
	// Remove ^, ~, >=, >, <=, <, = prefixes
	for _, prefix := range []string{"^", "~", ">=", ">", "<=", "<", "="} {
		if strings.HasPrefix(ver, prefix) {
			ver = strings.TrimPrefix(ver, prefix)
			break
		}
	}
	return strings.TrimSpace(ver)
}

// resolveCrate queries crates.io (or custom registry) for the latest version.
func (m *freshnessModule) resolveCrate(ctx context.Context, dep *Dependency) {
	ep := m.cfg.registryEndpoint(EcosystemCargo)
	baseURL := m.cfg.registryURL(EcosystemCargo, "https://crates.io/api/v1")
	url := fmt.Sprintf("%s/crates/%s", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url

	var resp cratesIOResponse
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		return
	}
	if resp.Crate.MaxVersion != "" {
		dep.Latest = resp.Crate.MaxVersion
	}
}

// buildLineIndex creates a map from content lines for lookup.
func buildLineIndex(data []byte) []string {
	return strings.Split(string(data), "\n")
}

// findLineForKey finds the approximate line number for a TOML key.
func findLineForKey(lines []string, key string) int {
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+" ") || strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, `"`+key+`"`) {
			return i + 1
		}
	}
	return 0
}
