package discovery

import (
	"context"
	"net/http"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// OSV ecosystem identifiers mapped from our internal ecosystem constants.
// See https://ossf.github.io/osv-schema/#affectedpackage-field
var osvEcosystemMap = map[string]string{
	supplychain.EcosystemGoMod:         "Go",
	supplychain.EcosystemNpm:           "npm",
	supplychain.EcosystemPip:           "PyPI",
	supplychain.EcosystemCargo:         "crates.io",
	supplychain.EcosystemAlpineAPK:     "Alpine",
	supplychain.EcosystemDebianAPT:     "Debian",
	supplychain.EcosystemDockerImage:   "", // no OSV ecosystem for container images
	supplychain.EcosystemGitHubRelease: "", // tools checked via GitHub advisories, not OSV
}

// osvQueryRequest is the POST body for api.osv.dev/v1/query.
type osvQueryRequest struct {
	Package *osvPackage `json:"package,omitempty"`
	Version string      `json:"version,omitempty"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

// osvBaseURL is the OSV API root. A package var (not a const) so tests can point the
// batch/hydrate calls at an httptest server.
var osvBaseURL = "https://api.osv.dev"

// osvBatchMax is OSV's documented ceiling on queries per /v1/querybatch request.
const osvBatchMax = 1000

// osvBatchRequest / osvBatchResponse model POST /v1/querybatch — the bulk endpoint that
// resolves up to 1000 package queries in ONE request. Results align by index with the
// submitted queries. Each result carries ID-ONLY vulnerability references (no severity /
// affected data); full details are hydrated per-ID via GET /v1/vulns/{id}.
type osvBatchRequest struct {
	Queries []osvQueryRequest `json:"queries"`
}

type osvBatchResponse struct {
	Results []osvBatchResult `json:"results"`
}

type osvBatchResult struct {
	Vulns []osvVulnRef `json:"vulns"`
}

type osvVulnRef struct {
	ID       string `json:"id"`
	Modified string `json:"modified"`
}

type osvVuln struct {
	ID       string        `json:"id"`
	Summary  string        `json:"summary"`
	Aliases  []string      `json:"aliases"`
	Severity []osvSeverity `json:"severity"`
	Affected []osvAffected `json:"affected"`
}

// osvDatabaseSpecif carries source-specific metadata. For RUSTSEC advisories, Informational
// ("unmaintained" | "unsound" | "notice") marks entries that are NOT vulnerabilities — they
// carry no CVSS severity. RUSTSEC sets it on each AFFECTED entry's database_specific (the
// vuln-level database_specific holds only license info, so it must be read here). osv-scanner
// surfaces these as warnings; freshness must not let severity_override escalate them to a
// CI-blocking critical.
type osvDatabaseSpecif struct {
	Informational string `json:"informational"`
}

// isInformational reports whether an advisory is a non-vulnerability notice (unmaintained,
// unsound, notice) rather than an exploitable flaw — true if any affected entry is so marked.
func (v osvVuln) isInformational() bool {
	for _, a := range v.Affected {
		if a.DatabaseSpecific.Informational != "" {
			return true
		}
	}
	return false
}

type osvSeverity struct {
	Type  string `json:"type"`  // "CVSS_V3", "CVSS_V2"
	Score string `json:"score"` // CVSS vector string
}

type osvAffected struct {
	Package          *osvPackage       `json:"package"`
	Ranges           []osvRange        `json:"ranges"`
	DatabaseSpecific osvDatabaseSpecif `json:"database_specific"`
}

type osvRange struct {
	Type   string     `json:"type"` // "ECOSYSTEM", "SEMVER", "GIT"
	Events []osvEvent `json:"events"`
}

type osvEvent struct {
	Introduced string `json:"introduced,omitempty"`
	Fixed      string `json:"fixed,omitempty"`
}

// correlateVulns populates each dependency's Vulnerabilities from the OSV database.
//
// It works in two phases over ALL OSV-eligible dependencies at once, rather than the
// former one-serial-request-per-package crawl:
//
//  1. Batch query — POST /v1/querybatch (chunked to OSV's 1000-query ceiling) resolves
//     every package in a single round trip, returning ID-only advisory references.
//  2. Hydrate — GET /v1/vulns/{id} fills severity/affected/aliases for the (usually
//     small) set of matched advisories, deduplicated so a shared advisory is fetched once.
//
// Every network call goes through doJSONRetry (per-attempt deadline + bounded retry over
// a PING-health-checked transport), so a stalled endpoint is recovered from, not fatal.
// When a call still fails after retries, the affected dependencies are marked
// VULN-UNVERIFIED (VulnScanError) — NEVER silently treated as clean. That coverage gap is
// surfaced loudly by the callers (audition / lint); this function only records the data.
func (m *Resolver) correlateVulns(ctx context.Context, deps []supplychain.Dependency) {
	if !m.cfg.vulnEnabled() {
		return
	}

	// Build the batch: one query per OSV-eligible dependency, remembering which dep and
	// version each query maps back to. Ecosystems OSV does not cover (docker images,
	// github-release tools) are out of scope by definition — skipped, not "unverified".
	type qref struct {
		depIdx  int
		version string
		osvEco  string
	}
	var queries []osvQueryRequest
	var refs []qref
	for i := range deps {
		osvEco, ok := osvEcosystemMap[deps[i].Ecosystem]
		if !ok || osvEco == "" {
			continue
		}
		depVersion := strings.TrimPrefix(deps[i].Current, "v")
		if depVersion == "" {
			continue
		}
		queries = append(queries, osvQueryRequest{
			Package: &osvPackage{Name: deps[i].Name, Ecosystem: osvEco},
			Version: depVersion,
		})
		refs = append(refs, qref{depIdx: i, version: depVersion, osvEco: osvEco})
	}
	if len(queries) == 0 {
		return
	}

	// Phase 1 — batch query, chunked. A chunk that fails after retries marks ITS deps
	// unverified; the rest of the fleet is unaffected.
	matchedIDs := make([][]osvVulnRef, len(queries)) // aligned with queries/refs
	for start := 0; start < len(queries); start += osvBatchMax {
		end := start + osvBatchMax
		if end > len(queries) {
			end = len(queries)
		}
		var resp osvBatchResponse
		err := m.http.doJSONRetry(ctx, http.MethodPost, osvBaseURL+"/v1/querybatch",
			osvBatchRequest{Queries: queries[start:end]}, &resp)
		if err != nil || len(resp.Results) != end-start {
			reason := "osv: batch result count mismatch"
			if err != nil {
				reason = err.Error()
			}
			for j := start; j < end; j++ {
				deps[refs[j].depIdx].VulnScanError = reason
			}
			continue
		}
		for j := start; j < end; j++ {
			matchedIDs[j] = resp.Results[j-start].Vulns
		}
	}

	// Phase 2 — hydrate matched advisory details (deduplicated). A detail fetch that
	// fails after retries marks the referencing dep unverified: we KNOW an advisory
	// applies but cannot classify it — the opposite of clean.
	details := make(map[string]*osvVuln)
	hydrate := func(id string) (*osvVuln, error) {
		if v, ok := details[id]; ok {
			return v, nil
		}
		var v osvVuln
		if err := m.http.doJSONRetry(ctx, http.MethodGet, osvBaseURL+"/v1/vulns/"+id, nil, &v); err != nil {
			return nil, err
		}
		details[id] = &v
		return &v, nil
	}

	for j := range refs {
		if len(matchedIDs[j]) == 0 {
			continue // no advisories, or the chunk already failed (dep marked above)
		}
		dep := &deps[refs[j].depIdx]
		for _, ref := range matchedIDs[j] {
			v, err := hydrate(ref.ID)
			if err != nil {
				dep.VulnScanError = err.Error()
				continue
			}
			m.applyVuln(dep, *v, refs[j].osvEco, refs[j].version)
		}
	}
}

// applyVuln filters one hydrated OSV advisory against the dependency and, if it is a
// genuine, in-scope, unfixed vulnerability, appends it to dep.Vulnerabilities.
func (m *Resolver) applyVuln(dep *supplychain.Dependency, v osvVuln, osvEco, depVersion string) {
	// Informational advisories (RUSTSEC unmaintained / unsound / notice) are not
	// vulnerabilities — they carry no CVSS. Don't count them as CVEs or let
	// severity_override escalate them to a blocking critical; osv-scanner already
	// surfaces them as warnings. (This is what made bincode's "unmaintained" notice
	// a false critical while osv correctly warned it.)
	if v.isInformational() {
		return
	}
	if !meetsMinSeverity(v.Severity, m.cfg.Vulnerability.MinSeverity) {
		return
	}
	vi := supplychain.VulnInfo{
		ID:       v.ID,
		Aliases:  v.Aliases,
		Summary:  v.Summary,
		Severity: extractHighestSeverity(v.Severity),
		FixedIn:  extractFixedVersion(v.Affected, dep.Name, osvEco),
		Source:   "osv",
	}
	// Skip vulns already fixed in the installed version.
	if vi.FixedIn != "" {
		delta := version.CompareDependencyVersions(depVersion, vi.FixedIn, dep.Ecosystem)
		if delta.Major < 0 || (delta.Major == 0 && delta.Minor < 0) || (delta.Major == 0 && delta.Minor == 0 && delta.Patch <= 0) {
			return
		}
	}
	dep.Vulnerabilities = append(dep.Vulnerabilities, vi)
}

// severityRank maps severity strings to numeric ranks for comparison.
var severityRank = map[string]int{
	"LOW":      1,
	"MODERATE": 2,
	"HIGH":     3,
	"CRITICAL": 4,
}

// meetsMinSeverity checks if any severity in the vuln meets the minimum threshold.
func meetsMinSeverity(severities []osvSeverity, minSev string) bool {
	if minSev == "" {
		return true
	}
	minRank := severityRank[strings.ToUpper(minSev)]
	if minRank == 0 {
		return true // unknown min → accept all
	}

	highest := extractHighestSeverity(severities)
	rank := severityRank[strings.ToUpper(highest)]
	if rank == 0 {
		// No CVSS score available — include by default (conservative).
		return true
	}
	return rank >= minRank
}

// extractHighestSeverity derives a severity label from CVSS vectors.
func extractHighestSeverity(severities []osvSeverity) string {
	bestScore := 0.0
	for _, s := range severities {
		if s.Type != "CVSS_V3" && s.Type != "CVSS_V2" {
			continue
		}
		score := parseCVSSBaseScore(s.Score)
		if score > bestScore {
			bestScore = score
		}
	}

	switch {
	case bestScore >= 9.0:
		return "CRITICAL"
	case bestScore >= 7.0:
		return "HIGH"
	case bestScore >= 4.0:
		return "MODERATE"
	case bestScore > 0:
		return "LOW"
	default:
		return "UNKNOWN"
	}
}

// parseCVSSBaseScore extracts the base score from a CVSS vector string.
// CVSS v3 vectors look like: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
// We compute a rough score from the vector components.
// For simplicity, if the vector doesn't parse, return 0.
func parseCVSSBaseScore(vector string) float64 {
	// OSV sometimes includes the score directly in a "score" field alongside
	// the vector. The CVSS vector string itself requires full computation.
	// For a practical approach, we use known severity patterns from the vector.
	v := strings.ToUpper(vector)

	// Count high-impact components as a rough severity estimate.
	var score float64
	if strings.Contains(v, "/AV:N") {
		score += 2.5 // network attack vector
	}
	if strings.Contains(v, "/AC:L") {
		score += 1.5 // low complexity
	}
	if strings.Contains(v, "/PR:N") {
		score += 1.5 // no privileges required
	}
	if strings.Contains(v, "/C:H") {
		score += 1.5 // high confidentiality impact
	}
	if strings.Contains(v, "/I:H") {
		score += 1.5 // high integrity impact
	}
	if strings.Contains(v, "/A:H") {
		score += 1.5 // high availability impact
	}

	return score
}

// extractFixedVersion finds the earliest fixed version from affected ranges.
func extractFixedVersion(affected []osvAffected, name, ecosystem string) string {
	for _, a := range affected {
		if a.Package == nil {
			continue
		}
		if !strings.EqualFold(a.Package.Name, name) || a.Package.Ecosystem != ecosystem {
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
	return ""
}
