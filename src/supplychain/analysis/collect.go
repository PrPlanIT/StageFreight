package analysis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// collectObservations gathers advisory observations from every source, WITHOUT
// running any new OSV-API query: source (a) reads the vulnerabilities already
// correlated onto the Snapshot's dependencies during discovery (Source
// "osv-api"), and source (b) runs osv-scanner over the repository's lockfiles
// (Source "osv-scanner"). The osv-scanner error is returned so callers may
// surface it, but observations gathered so far are always returned.
func collectObservations(ctx context.Context, snapshot *supplychain.Snapshot, cfg Config) ([]AdvisoryObservation, error) {
	var obs []AdvisoryObservation

	// (a) OSV-API — already correlated onto the snapshot's dependencies.
	if snapshot != nil {
		for _, dep := range snapshot.Dependencies {
			for _, v := range dep.Vulnerabilities {
				if v.ID == "" {
					continue
				}
				obs = append(obs, AdvisoryObservation{
					Source:    "osv-api",
					VulnID:    v.ID,
					Package:   dep.Name,
					Ecosystem: dep.Ecosystem,
					Severity:  normalizeLabel(v.Severity),
					FixedIn:   v.FixedIn,
					Summary:   v.Summary,
					File:      dep.File,
					Line:      dep.Line,
				})
			}
		}
	}

	// (b) osv-scanner.
	scannerObs, err := scanOSV(ctx, cfg)
	obs = append(obs, scannerObs...)
	return obs, err
}

// scanOSV runs the (already-resolved) osv-scanner binary over every
// non-dominated lockfile beneath cfg.RootDir, producing observations. An empty
// ScannerBinPath skips the source silently. Relocated from the former osv lint
// module (binary resolution now happens in the caller).
func scanOSV(ctx context.Context, cfg Config) ([]AdvisoryObservation, error) {
	if cfg.ScannerBinPath == "" {
		return nil, nil
	}

	root := cfg.RootDir
	if root == "" {
		root, _ = os.Getwd()
	}

	lockfilePaths := discoverLockfiles(root)
	if len(lockfilePaths) == 0 {
		return nil, nil
	}

	var obs []AdvisoryObservation
	for _, abs := range lockfilePaths {
		rel, relErr := filepath.Rel(root, abs)
		if relErr != nil {
			rel = abs
		}
		o, scanErr := scanLockfile(ctx, cfg.ScannerBinPath, cfg.ScannerEnv, abs, rel)
		if scanErr != nil {
			return obs, scanErr
		}
		obs = append(obs, o...)
	}
	return obs, nil
}

// scanLockfile runs osv-scanner against a single lockfile and converts its
// grouped JSON output into observations, attributing each to rel (the
// repo-relative lockfile path).
func scanLockfile(ctx context.Context, binPath string, env []string, absPath, rel string) ([]AdvisoryObservation, error) {
	cmd := exec.CommandContext(ctx, binPath, "scan", "--format", "json", "-L", absPath)
	cmd.Env = env
	out, err := cmd.Output()

	// osv-scanner exits 1 when vulnerabilities are found — still valid JSON.
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("osv-scanner: %w", err)
	}

	var report osvReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("osv-scanner: parsing output: %w", err)
	}

	var obs []AdvisoryObservation
	for _, res := range report.Results {
		for _, pkg := range res.Packages {
			for _, group := range pkg.Groups {
				if len(group.IDs) == 0 {
					continue
				}
				primaryID := group.IDs[0]
				obs = append(obs, AdvisoryObservation{
					Source:    "osv-scanner",
					VulnID:    primaryID,
					Aliases:   groupAliases(group),
					Package:   pkg.Package.Name,
					Ecosystem: pkg.Package.Ecosystem,
					Severity:  scoreToLabel(group.MaxSeverity),
					FixedIn:   vulnFixedIn(primaryID, pkg.Vulnerabilities, pkg.Package.Name),
					Summary:   vulnSummary(group.IDs, pkg.Vulnerabilities),
					File:      rel,
				})
			}
		}
	}
	return obs, nil
}

// discoverLockfiles walks root for osv-scanner-supported lockfiles, skipping
// hidden directories (mirroring the lint engine's file collection) and dominated
// (nested/vendored) lockfiles. Returns absolute paths.
func discoverLockfiles(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		base := filepath.Base(path)
		if !lockfiles[base] {
			return nil
		}
		if isDominatedLockfile(path, base) {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths
}

// isDominatedLockfile reports whether a same-named lockfile exists in an ancestor
// directory ABOVE this file's own directory — marking this one as a nested
// sub-lockfile (a vendored crate's lock, a sub-project) rather than the top-level
// build graph. Relocated from the former osv lint module.
func isDominatedLockfile(absPath, base string) bool {
	dir := filepath.Dir(filepath.Dir(absPath)) // start above the file's own directory
	for i := 0; i < 32; i++ {
		if _, err := os.Stat(filepath.Join(dir, base)); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

// lockfiles maps base filenames osv-scanner supports.
var lockfiles = map[string]bool{
	"go.mod":            true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
	"Cargo.lock":        true,
	"requirements.txt":  true,
	"poetry.lock":       true,
	"Pipfile.lock":      true,
	"composer.lock":     true,
	"Gemfile.lock":      true,
	"pubspec.lock":      true,
	"pom.xml":           true,
	"gradle.lockfile":   true,
}

// osvReport is the top-level osv-scanner v2 JSON output. Relocated from the
// former osv lint module.
type osvReport struct {
	Results []osvResult `json:"results"`
}

type osvResult struct {
	Source   osvSource    `json:"source"`
	Packages []osvPackage `json:"packages"`
}

type osvSource struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type osvPackage struct {
	Package         osvPkgInfo `json:"package"`
	Vulnerabilities []osvVuln  `json:"vulnerabilities"`
	Groups          []osvGroup `json:"groups"`
}

type osvPkgInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Ecosystem string `json:"ecosystem"`
}

type osvVuln struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary"`
	Aliases  []string `json:"aliases"`
	Affected []struct {
		Ranges []struct {
			Events []struct {
				Fixed string `json:"fixed,omitempty"`
			} `json:"events"`
		} `json:"ranges"`
		Package *osvPkgInfo `json:"package"`
	} `json:"affected"`
	DatabaseSpecific map[string]any `json:"database_specific"`
}

type osvGroup struct {
	IDs         []string `json:"ids"`
	Aliases     []string `json:"aliases"`
	MaxSeverity string   `json:"max_severity"`
}

// groupAliases returns every identifier for a group other than its primary ID
// (the remaining grouped IDs plus the group's explicit aliases), de-duplicated.
// These feed canonicalize's id-set intersection so an osv-scanner group and an
// OSV-API observation that name the same advisory under different IDs merge.
func groupAliases(group osvGroup) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for i, id := range group.IDs {
		if i == 0 {
			continue // primary — carried as VulnID
		}
		add(id)
	}
	for _, a := range group.Aliases {
		add(a)
	}
	return out
}

// vulnSummary finds the summary for any of the IDs in a group. Relocated from
// the former osv lint module.
func vulnSummary(ids []string, vulns []osvVuln) string {
	idSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	for _, v := range vulns {
		if idSet[v.ID] && v.Summary != "" {
			return v.Summary
		}
	}
	return ""
}

// vulnFixedIn extracts the earliest fixed version for the given vuln/package.
// Relocated from the former osv lint module.
func vulnFixedIn(id string, vulns []osvVuln, pkgName string) string {
	for _, v := range vulns {
		if v.ID != id {
			continue
		}
		for _, a := range v.Affected {
			if a.Package != nil && !strings.EqualFold(a.Package.Name, pkgName) {
				continue
			}
			for _, r := range a.Ranges {
				for _, e := range r.Events {
					if e.Fixed != "" {
						return e.Fixed
					}
				}
			}
		}
	}
	return ""
}

// scoreToLabel maps an osv-scanner CVSS numeric score string to the OSV severity
// label vocabulary. The thresholds reproduce the former osv module's
// severityFromScore→lint.Severity mapping once run through evaluate: ≥9/≥7 →
// CRITICAL/HIGH → critical, ≥4 → MODERATE → warning, else → info. An unparseable
// score preserves severityFromScore's warning default (→ MODERATE).
func scoreToLabel(score string) string {
	f, err := strconv.ParseFloat(score, 64)
	if err != nil {
		return "MODERATE"
	}
	switch {
	case f >= 9.0:
		return "CRITICAL"
	case f >= 7.0:
		return "HIGH"
	case f >= 4.0:
		return "MODERATE"
	case f > 0:
		return "LOW"
	default:
		return "UNKNOWN"
	}
}

// normalizeLabel upper-cases an OSV-API severity label to the canonical
// vocabulary; empty stays empty (treated as UNKNOWN by ranking/evaluate).
func normalizeLabel(label string) string {
	return strings.ToUpper(strings.TrimSpace(label))
}
