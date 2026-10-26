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
)

// npmFullDoc is the package document — dist-tags, the version list, and per-version
// publish times (the last consulted only when a cooldown is configured).
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
	for name, spec := range pkg.Dependencies {
		ver := stripNpmRange(spec)
		if ver == "" {
			continue
		}
		deps = append(deps, supplychain.Dependency{
			Name:       name,
			Current:    ver,
			Constraint: spec, // declared range (operator included) — bounds the eligible upgrade
			Ecosystem:  supplychain.EcosystemNpm,
			File:       file.Path,
			Line:       findLineForJSON(lines, "dependencies", name),
		})
	}

	for name, spec := range pkg.DevDependencies {
		ver := stripNpmRange(spec)
		if ver == "" {
			continue
		}
		deps = append(deps, supplychain.Dependency{
			Name:       name,
			Current:    ver,
			Constraint: spec,
			Ecosystem:  supplychain.EcosystemNpm,
			File:       file.Path,
			Line:       findLineForJSON(lines, "devDependencies", name),
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

// resolveNpmPackage queries the npm registry for the latest version, the version list,
// and each version's publish time (for the cooldown).
func (m *Resolver) resolveNpmPackage(ctx context.Context, dep *supplychain.Dependency) {
	ep := m.cfg.registryEndpoint(supplychain.EcosystemNpm)
	baseURL := m.cfg.registryURL(supplychain.EcosystemNpm, "https://registry.npmjs.org")

	// Fetch the package document: dist-tags, the full version list, and per-version
	// publish times. The version list is required to resolve the in-range eligible
	// target and hold out-of-range majors for review.
	url := fmt.Sprintf("%s/%s", strings.TrimRight(baseURL, "/"), dep.Name)
	dep.SourceURL = url
	var doc npmFullDoc
	if err := m.http.fetchJSON(ctx, url, &doc, ep); err != nil {
		return
	}
	dep.Latest = doc.DistTags["latest"]

	// In-range eligible target + version list, mirroring cargo.go: the newest version
	// satisfying the DECLARED range. A higher out-of-range version is a
	// constraint-expanding major, held for review — never auto-applied.
	nums := make([]string, 0, len(doc.Versions))
	for v := range doc.Versions {
		nums = append(nums, v)
		if t, err := time.Parse(time.RFC3339, doc.Time[v]); err == nil {
			setReleased(dep, v, t)
		}
	}
	dep.LatestEligible = version.LatestSatisfying(dep.Constraint, nums)
	dep.AvailableVersions = nums
}

// findLineForJSON finds the line of dependency `key` within the given top-level
// section ("dependencies" / "devDependencies") of package.json. Scoping to the
// section AND requiring the match to be a JSON key (followed by ':') avoids
// mis-anchoring to an earlier VALUE equal to the name — e.g. a `"dev": "vite"`
// script line sitting above the real `"vite":` devDependency entry, which would
// otherwise misreport the finding's line and defeat the operator-preserving
// auto-bump (it reads the manifest at this line). Returns 0 if not found.
func findLineForJSON(lines []string, section, key string) int {
	sectionHeader := `"` + section + `":`
	target := `"` + key + `"`
	inSection := false
	depth := 0
	for i, line := range lines {
		if !inSection {
			if strings.Contains(line, sectionHeader) {
				inSection = true
				depth = strings.Count(line, "{") - strings.Count(line, "}")
			}
			continue
		}
		if idx := strings.Index(line, target); idx >= 0 &&
			strings.HasPrefix(strings.TrimSpace(line[idx+len(target):]), ":") {
			return i + 1
		}
		depth += strings.Count(line, "{") - strings.Count(line, "}")
		if depth <= 0 {
			break // reached the end of the section object
		}
	}
	return 0
}
