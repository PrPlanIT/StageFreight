package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/lint"
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"

	masterminds "github.com/Masterminds/semver/v3"
)

// npmRegistryResponse matches the npm registry abbreviated response.
type npmRegistryResponse struct {
	Version string `json:"version"`
}

// npmFullDoc is the full package document — fetched only when a cooldown is configured,
// because it carries per-version publish times under "time".
type npmFullDoc struct {
	DistTags map[string]string          `json:"dist-tags"`
	Time     map[string]string          `json:"time"` // version → RFC3339 (plus "created"/"modified")
	Versions map[string]json.RawMessage `json:"versions"`
}

// checkNpm parses package.json and resolves latest versions via registry.npmjs.org.
func (m *Resolver) checkNpm(ctx context.Context, file lint.FileInfo) ([]supplychain.Dependency, error) {
	if !m.cfg.SourceEnabled(supplychain.EcosystemNpm) {
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

	var deps []supplychain.Dependency
	for name, version := range pkg.Dependencies {
		ver := stripNpmRange(version)
		if ver == "" {
			continue
		}
		deps = append(deps, supplychain.Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: supplychain.EcosystemNpm,
			File:      file.Path,
			Line:      findLineForJSON(lines, name),
		})
	}

	for name, version := range pkg.DevDependencies {
		ver := stripNpmRange(version)
		if ver == "" {
			continue
		}
		deps = append(deps, supplychain.Dependency{
			Name:      name,
			Current:   ver,
			Ecosystem: supplychain.EcosystemNpm,
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

// resolveNpmPackage queries the npm registry for the latest version. With a MinReleaseAge
// cooldown configured, it recommends the newest STABLE release old enough to clear the
// window instead of the bleeding edge — the supply-chain safeguard.
func (m *Resolver) resolveNpmPackage(ctx context.Context, dep *supplychain.Dependency) {
	ep := m.cfg.registryEndpoint(supplychain.EcosystemNpm)
	baseURL := m.cfg.registryURL(supplychain.EcosystemNpm, "https://registry.npmjs.org")

	// No cooldown → the abbreviated /latest endpoint is enough and cheap.
	if m.cfg.minReleaseAge() <= 0 {
		url := fmt.Sprintf("%s/%s/latest", strings.TrimRight(baseURL, "/"), dep.Name)
		dep.SourceURL = url
		var resp npmRegistryResponse
		if err := m.http.fetchJSON(ctx, url, &resp, ep); err != nil {
			return
		}
		if resp.Version != "" {
			dep.Latest = resp.Version
		}
		return
	}

	// Cooldown active → fetch the full document (per-version publish times) and pick the
	// newest stable version published before the cutoff.
	url := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url
	var doc npmFullDoc
	if err := m.http.fetchJSON(ctx, url, &doc, ep); err != nil {
		return
	}
	latest := doc.DistTags["latest"]
	aged := agedLatestNpm(doc, m.clock().Add(-m.cfg.minReleaseAge()))
	switch {
	case aged != "":
		dep.Latest = aged
		if latest != "" && latest != aged {
			dep.CooldownHeld = latest // a newer release exists but is still within the cooldown
		}
	case latest != "":
		dep.Latest = latest // nothing aged enough yet (brand-new package) — don't regress
	}
}

// agedLatestNpm returns the highest STABLE version published at or before cutoff — the
// newest release old enough to clear the cooldown. Empty if none qualify.
func agedLatestNpm(doc npmFullDoc, cutoff time.Time) string {
	best := ""
	var bestV *masterminds.Version
	for ver, ts := range doc.Time {
		if ver == "created" || ver == "modified" {
			continue
		}
		if _, real := doc.Versions[ver]; !real && len(doc.Versions) > 0 {
			continue // a time entry with no matching version (defensive)
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil || t.After(cutoff) {
			continue // unparseable, or still inside the cooldown window
		}
		v := version.ParseVersion(ver)
		if v == nil || v.Prerelease() != "" {
			continue // stable releases only
		}
		if bestV == nil || v.GreaterThan(bestV) {
			bestV, best = v, ver
		}
	}
	return best
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
