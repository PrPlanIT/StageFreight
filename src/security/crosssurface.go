package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// CrossSurfaceResult is the reconciliation of this image scan against the
// source-side Assessment the audition persisted: every advisory collapsed by ID
// with the surface(s) it was observed on.
type CrossSurfaceResult struct {
	Vulnerabilities []analysis.Vulnerability
	SourceOnly      int  // observed only in source (manifests/lockfiles), not the image
	ImageOnly       int  // observed only in the built image, not in source
	Both            int  // observed on both surfaces
	SourceFound     bool // the audition's source catalogue was present and read
}

// CrossSurface reconciles the image scan's vulnerabilities with the source-side
// Assessment the audition wrote to .stagefreight/security/source-vulns.json,
// collapsing both by advisory ID (via analysis.Reduce, the same canonicalization
// the audition uses) so each vulnerability records whether it was seen in source,
// in the image, or both.
//
// Strictly additive and read-only with respect to the image scan: the source
// catalogue is optional (absent → image-only), a parse failure degrades to
// image-only, and this never mutates the caller's scan result. Returns nil when
// there is nothing to reconcile.
func CrossSurface(rootDir string, imageVulns []Vulnerability) *CrossSurfaceResult {
	var obs []analysis.AdvisoryObservation

	// Image leg: this scan's findings, tagged as the image surface.
	for _, v := range imageVulns {
		if v.ID == "" {
			continue
		}
		obs = append(obs, analysis.AdvisoryObservation{
			Source:   v.Source, // "trivy" | "grype"
			Surface:  analysis.SurfaceImage,
			VulnID:   v.ID,
			Package:  v.Package,
			Version:  v.Installed,
			Severity: v.Severity,
			FixedIn:  v.FixedIn,
			Summary:  v.Description,
		})
	}

	// Source leg: the audition catalogue, if present.
	src := readSourceCatalogue(rootDir)
	if src != nil {
		for _, r := range src.Vulnerabilities {
			if r.ID == "" {
				continue
			}
			name, ver := splitPkgVersion(firstOf(r.Packages))
			obs = append(obs, analysis.AdvisoryObservation{
				Source:   "source",
				Surface:  analysis.SurfaceSource,
				VulnID:   r.ID,
				Aliases:  r.Aliases,
				Package:  name,
				Version:  ver,
				Severity: r.Severity,
			})
		}
	}

	if len(obs) == 0 {
		return nil
	}

	vulns := analysis.Reduce(obs)
	if src != nil {
		attachSourceReachability(vulns, src)
	}
	res := &CrossSurfaceResult{Vulnerabilities: vulns, SourceFound: src != nil}
	for _, v := range vulns {
		switch surfaceClass(v.Surfaces) {
		case "both":
			res.Both++
		case "source":
			res.SourceOnly++
		case "image":
			res.ImageOnly++
		}
	}
	return res
}

// DisclosureLines returns human-readable rows for the review section: the
// advisories observed on BOTH surfaces — the cross-surface overlap worth
// highlighting (a vulnerable module in source AND compiled into the image) —
// each with its id and, when carried from source, its reachability verdict.
// Capped so the section stays readable; the full set lives in cross-surface.json.
func (r *CrossSurfaceResult) DisclosureLines() []string {
	const limit = 8
	var lines []string
	for _, v := range r.Vulnerabilities {
		if surfaceClass(v.Surfaces) != "both" {
			continue
		}
		if len(lines) >= limit {
			break
		}
		line := v.ID + " [source+image]"
		if rr, ok := reachabilityOf(v); ok && rr.State != evidence.ReachUnknown {
			line += " [" + rr.State.String() + "]"
		}
		lines = append(lines, line)
	}
	if r.Both > limit {
		lines = append(lines, fmt.Sprintf("… and %d more on both surfaces", r.Both-limit))
	}
	return lines
}

// reachabilityOf returns the reachability evidence attached to a collapsed
// vulnerability, if any.
func reachabilityOf(v analysis.Vulnerability) (evidence.ReachabilityEvidence, bool) {
	for _, e := range v.Evidence {
		if r, ok := e.(evidence.ReachabilityEvidence); ok {
			return r, true
		}
	}
	return evidence.ReachabilityEvidence{}, false
}

// Marshal renders the reconciled vulnerabilities as the cross-surface catalogue
// artifact, reusing the source-assessment JSON shape (which already carries the
// surfaces field).
func (r *CrossSurfaceResult) Marshal() ([]byte, error) {
	return analysis.MarshalSourceAssessment(r.Vulnerabilities)
}

// attachSourceReachability re-attaches the reachability evidence that source
// records carry onto the collapsed vulnerabilities, matched by any shared
// identifier (canonical ID or alias), so the cross-surface view and artifact
// preserve WHY a source advisory was downgraded. The observation legs cannot
// carry the Evidence interface through canonicalize, so it is restored here.
func attachSourceReachability(vulns []analysis.Vulnerability, src *analysis.SourceAssessment) {
	// Index reachability by each source record's PRIMARY id only. A collapsed vuln
	// matches a record only when the record's primary id is in the vuln's
	// identifier set — the same conservative primary-containment rule canonicalize
	// uses (knowledge.go). Keying by aliases too would re-introduce the collision
	// canonicalize deliberately avoids: two distinct advisories that merely share
	// a non-primary CVE alias would cross-contaminate each other's reachability.
	byPrimary := map[string]*analysis.ReachabilityRecord{}
	for i := range src.Vulnerabilities {
		r := &src.Vulnerabilities[i]
		if r.Reachability != nil && r.ID != "" {
			byPrimary[r.ID] = r.Reachability
		}
	}
	if len(byPrimary) == 0 {
		return
	}
	for i := range vulns {
		rr := reachForVuln(byPrimary, vulns[i])
		if rr == nil {
			continue
		}
		vulns[i].Evidence = append(vulns[i].Evidence, evidence.ReachabilityEvidence{
			State:      evidence.ParseReachabilityState(rr.State),
			Analyzer:   rr.Analyzer,
			Confidence: evidence.ParseConfidence(rr.Confidence),
			Facts:      rr.Facts,
		})
	}
}

// reachForVuln returns the reachability record whose source advisory is the SAME
// as v — its primary id equals v's canonical id or one of v's aliases. Because a
// merged source observation contributes its primary id to v's identifier set,
// this reliably matches the same advisory while never matching a distinct one
// that only shares a non-primary alias.
func reachForVuln(byPrimary map[string]*analysis.ReachabilityRecord, v analysis.Vulnerability) *analysis.ReachabilityRecord {
	if rr := byPrimary[v.ID]; rr != nil {
		return rr
	}
	for _, a := range v.Aliases {
		if rr := byPrimary[a]; rr != nil {
			return rr
		}
	}
	return nil
}

// readSourceCatalogue loads the audition's source-vulns.json, returning nil if it
// is absent or unparseable (both are non-fatal — the reconciliation degrades to
// image-only).
func readSourceCatalogue(rootDir string) *analysis.SourceAssessment {
	data, err := os.ReadFile(filepath.Join(rootDir, ".stagefreight", "security", "source-vulns.json"))
	if err != nil {
		return nil
	}
	sa, err := analysis.UnmarshalSourceAssessment(data)
	if err != nil {
		return nil
	}
	return sa
}

// surfaceClass classifies a collapsed vulnerability's surface set.
func surfaceClass(surfaces []analysis.Surface) string {
	var hasSrc, hasImg bool
	for _, s := range surfaces {
		switch s {
		case analysis.SurfaceSource:
			hasSrc = true
		case analysis.SurfaceImage:
			hasImg = true
		}
	}
	switch {
	case hasSrc && hasImg:
		return "both"
	case hasSrc:
		return "source"
	case hasImg:
		return "image"
	default:
		return ""
	}
}

func firstOf(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	return ss[0]
}

// splitPkgVersion splits a "name@version" package string on its last "@"; returns
// (name, "") when there is no version suffix.
func splitPkgVersion(s string) (name, version string) {
	// at > 0 (not >= 0): a leading "@" is a scoped-npm name prefix (e.g.
	// "@scope/pkg" with no version), NOT a version separator — splitting there
	// would yield an empty name.
	if at := strings.LastIndex(s, "@"); at > 0 {
		return s[:at], s[at+1:]
	}
	return s, ""
}
