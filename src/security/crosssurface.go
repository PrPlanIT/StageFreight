package security

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
)

// CrossSurfaceResult is the reconciliation of this image scan against the
// source-side Assessment the audition persisted: every advisory collapsed by ID
// with the surface(s) it was observed on.
type CrossSurfaceResult struct {
	Vulnerabilities []analysis.Vulnerability
	SourceOnly      int // observed only in source (manifests/lockfiles), not the image
	ImageOnly       int // observed only in the built image, not in source
	Both            int // observed on both surfaces
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
	if src := readSourceCatalogue(rootDir); src != nil {
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
	res := &CrossSurfaceResult{Vulnerabilities: vulns}
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

// Marshal renders the reconciled vulnerabilities as the cross-surface catalogue
// artifact, reusing the source-assessment JSON shape (which already carries the
// surfaces field).
func (r *CrossSurfaceResult) Marshal() ([]byte, error) {
	return analysis.MarshalSourceAssessment(r.Vulnerabilities)
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
	if at := strings.LastIndex(s, "@"); at >= 0 {
		return s[:at], s[at+1:]
	}
	return s, ""
}
