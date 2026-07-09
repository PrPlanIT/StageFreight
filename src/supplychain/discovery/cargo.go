package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"

	toml "github.com/pelletier/go-toml/v2"
)

// cratesIOResponse matches the crates.io API response.
type cratesIOResponse struct {
	Crate struct {
		MaxVersion string `json:"max_version"`
	} `json:"crate"`
	Versions []struct {
		Num    string `json:"num"`
		Yanked bool   `json:"yanked"`
	} `json:"versions"`
}

// checkCargo parses Cargo.toml and resolves latest versions via crates.io.
func (m *Resolver) checkCargo(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	if !m.cfg.SourceEnabled(supplychain.EcosystemCargo) {
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
	var deps []supplychain.Dependency
	lines := buildLineIndex(data)

	for name, spec := range cargo.Dependencies {
		ver := extractCargoVersion(spec)
		if ver == "" {
			continue
		}
		deps = append(deps, supplychain.Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: supplychain.EcosystemCargo,
			File:      file.Path,
			Line:      findLineForKey(lines, name),
		})
	}

	for name, spec := range cargo.DevDependencies {
		ver := extractCargoVersion(spec)
		if ver == "" {
			continue
		}
		deps = append(deps, supplychain.Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: supplychain.EcosystemCargo,
			File:      file.Path,
			Line:      findLineForKey(lines, name),
		})
	}

	// Reconcile against the resolved lockfile. A committed Cargo.lock is the version that
	// actually ships, so currency and CVE correlation must be assessed against it — not the
	// Cargo.toml constraint floor. A loose `tar = "0.4"` pin locked to a patched 0.4.46 is
	// NOT vulnerable; flagging it would be a false critical that blocks CI on a fix that is
	// already present. The manifest line is still where we report; only the assessed version
	// changes. No lock (a library) → keep the declared constraint (intent).
	// (The lock lives at the workspace root, possibly several dirs above a member crate's
	// Cargo.toml, so we walk up to find it.)
	if locked := loadCargoLockVersions(findNearestFile(filepath.Dir(file.AbsPath), "Cargo.lock")); locked != nil {
		for i := range deps {
			if vers := locked[deps[i].Name]; len(vers) > 0 {
				if resolved := version.LatestEligibleSemver(deps[i].Current, vers); resolved != "" {
					deps[i].Current = resolved
				}
			}
		}
	}

	// Resolve latest versions
	for i := range deps {
		m.resolveCrate(ctx, &deps[i])
	}

	return deps, nil
}

// loadCargoLockVersions parses a Cargo.lock into name → resolved versions (a crate can
// appear at several versions; the caller picks the one matching each manifest constraint).
// Returns nil if the lock is absent or unparseable — callers then keep the manifest pin.
func loadCargoLockVersions(path string) map[string][]string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
		} `toml:"package"`
	}
	if err := toml.Unmarshal(data, &lock); err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, p := range lock.Package {
		if p.Name != "" && p.Version != "" {
			out[p.Name] = append(out[p.Name], p.Version)
		}
	}
	return out
}

// findNearestFile walks up from startDir looking for `name` (a member crate's Cargo.toml
// sits below the workspace-root Cargo.lock). Returns "" if not found within a bounded walk.
func findNearestFile(startDir, name string) string {
	dir := startDir
	for i := 0; i < 16; i++ {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
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
func (m *Resolver) resolveCrate(ctx context.Context, dep *supplychain.Dependency) {
	ep := m.cfg.registryEndpoint(supplychain.EcosystemCargo)
	baseURL := m.cfg.registryURL(supplychain.EcosystemCargo, "https://crates.io/api/v1")
	url := fmt.Sprintf("%s/crates/%s", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url

	var resp cratesIOResponse
	if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
		// A lookup failure is UNRESOLVED, not up-to-date — record it so the
		// degraded state survives into classification and reporting.
		dep.ResolutionError = "crates.io lookup failed: " + err.Error()
		return
	}
	if resp.Crate.MaxVersion == "" {
		dep.ResolutionError = "crates.io returned no version for " + dep.Name
		return
	}
	dep.Latest = resp.Crate.MaxVersion // latest AVAILABLE

	// Latest COMPATIBLE target: highest non-yanked version satisfying the caret of the
	// current pin (cargo's bare "0.12.22" is an implicit ^0.12.22). A higher
	// out-of-range version (e.g. 0.13.x for a 0.12 pin) is a major upgrade, held for
	// review — never auto-applied.
	var nums []string
	for _, v := range resp.Versions {
		if !v.Yanked {
			nums = append(nums, v.Num)
		}
	}
	dep.LatestEligible = version.LatestEligibleSemver(dep.Current, nums)
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
