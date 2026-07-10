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

// ObserveDependencies turns the OSV-API vulnerabilities already correlated onto
// deps (during discovery) into advisory observations (Source "osv-api"). Per-file
// callers pass the dependency subset for one manifest; deps with no correlated
// vulnerability contribute nothing. Policy-free: the caller applies any ignore /
// source-toggle filtering before handing deps in.
func ObserveDependencies(deps []supplychain.Dependency) []AdvisoryObservation {
	var obs []AdvisoryObservation
	for _, dep := range deps {
		for _, v := range dep.Vulnerabilities {
			if v.ID == "" {
				continue
			}
			obs = append(obs, AdvisoryObservation{
				Source:    "osv-api",
				VulnID:    v.ID,
				Aliases:   v.Aliases,
				Package:   dep.Name,
				Version:   dep.Current,
				Ecosystem: dep.Ecosystem,
				Severity:  normalizeLabel(v.Severity),
				FixedIn:   v.FixedIn,
				Summary:   v.Summary,
				File:      dep.File,
				Line:      dep.Line,
				Surface:   SurfaceSource,
			})
		}
	}
	return obs
}

// IsScannableLockfile reports whether osv-scanner should run over the file at
// absPath (base is its filename): it must be a supported lockfile and not a
// nested/dominated one (a vendored crate's lock, a sub-project). Mirrors the
// former osv lint module's per-file gate.
func IsScannableLockfile(base, absPath string) bool {
	if !lockfiles[base] {
		return false
	}
	return !isDominatedLockfile(absPath, base)
}

// ObserveScanner runs the resolved osv-scanner binary over one lockfile at
// absPath and returns its advisory observations (Source "osv-scanner"),
// attributing each to relPath. Callers gate the file with IsScannableLockfile
// first. Per-file counterpart used by the vulnerabilities lint module.
func ObserveScanner(ctx context.Context, binPath string, env []string, absPath, relPath string) ([]AdvisoryObservation, error) {
	return scanLockfile(ctx, binPath, env, absPath, relPath)
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
					Version:   pkg.Package.Version,
					Ecosystem: pkg.Package.Ecosystem,
					Severity:  scoreToLabel(group.MaxSeverity),
					FixedIn:   vulnFixedIn(primaryID, pkg.Vulnerabilities, pkg.Package.Name),
					Summary:   vulnSummary(group.IDs, pkg.Vulnerabilities),
					File:      rel,
					Surface:   SurfaceSource,
				})
			}
		}
	}
	return obs, nil
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
