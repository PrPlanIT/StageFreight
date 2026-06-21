package freshness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// Reconcile against the resolved lockfile: package-lock.json is what actually installs,
	// so currency and CVE correlation must use the locked version, not the package.json
	// range floor. A "^8.5.3" pin locked to a patched 8.5.15 is NOT vulnerable; flagging it
	// would be a false critical for a fix already present. npm flattens to one top-level
	// version per dependency, so this is a direct lookup (no caret resolution like cargo).
	if locked := loadNpmLockVersions(findNearestFile(filepath.Dir(file.AbsPath), "package-lock.json")); locked != nil {
		for i := range deps {
			if v := locked[deps[i].Name]; v != "" {
				deps[i].Current = v
			}
		}
	}

	// Resolve latest versions
	for i := range deps {
		m.resolveNpmPackage(ctx, &deps[i])
	}

	return deps, nil
}

// loadNpmLockVersions parses package-lock.json into name → installed top-level version.
// Handles lockfileVersion 2/3 (the "packages" map keyed by node_modules path) and the v1
// "dependencies" map. Returns nil if absent/unparseable — callers keep the manifest range.
func loadNpmLockVersions(path string) map[string]string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lock struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"` // v2/v3
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"` // v1
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil
	}
	out := map[string]string{}
	// v2/v3: only TOP-LEVEL entries ("node_modules/<name>", no further nesting) are the
	// versions a package.json dependency resolves to.
	for key, p := range lock.Packages {
		if p.Version == "" {
			continue
		}
		if name, ok := npmTopLevelName(key); ok {
			out[name] = p.Version
		}
	}
	// v1 fallback: "dependencies" is already top-level keyed by name.
	for name, p := range lock.Dependencies {
		if p.Version != "" {
			if _, seen := out[name]; !seen {
				out[name] = p.Version
			}
		}
	}
	return out
}

// npmTopLevelName extracts the package name from a v2/v3 lock key, but ONLY for a top-level
// install ("node_modules/<name>" with no further "node_modules/" nesting). Scopes are kept
// ("node_modules/@scope/pkg" → "@scope/pkg"); transitive copies are ignored.
func npmTopLevelName(key string) (string, bool) {
	const p = "node_modules/"
	if !strings.HasPrefix(key, p) {
		return "", false
	}
	rest := key[len(p):]
	if rest == "" || strings.Contains(rest, "node_modules/") {
		return "", false
	}
	return rest, true
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
