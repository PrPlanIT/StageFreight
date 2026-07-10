// Package analysis is the supply-chain vulnerability analysis layer. It takes
// raw per-source advisory observations (the OSV-API correlation already attached
// to a dependency, plus a per-file osv-scanner run) and reduces them to ONE
// canonical vulnerability per advisory with ONE verdict, so a given advisory is
// reported exactly once regardless of how many sources observed it.
//
// The pipeline is observe → canonicalize → evaluate:
//
//   - ObserveDependencies / ObserveScanner gather AdvisoryObservations from each
//     source (policy-free).
//   - canonicalize groups observations that describe the SAME advisory (one
//     observation's primary id is contained in another's id-set) into a single
//     Vulnerability — pure, deterministic, policy-free.
//   - evaluate assigns exactly one Verdict per Vulnerability from its severity.
//
// Reduce composes canonicalize → evaluate. Rendering to lint findings lives
// OUTSIDE this package (src/lint/modules/vulnerabilities) so analysis carries no
// dependency on the lint layer.
package analysis

import "github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"

// Verdict is the analysis-layer severity classification for a canonical
// vulnerability — the single verdict rendered per advisory. Its tiers mirror the
// lint severity tiers (info / warning / critical) WITHOUT importing lint; the
// render layer maps a Verdict to a lint.Severity one-to-one.
type Verdict int

const (
	VerdictInfo Verdict = iota
	VerdictWarning
	VerdictCritical
)

func (v Verdict) String() string {
	switch v {
	case VerdictWarning:
		return "warning"
	case VerdictCritical:
		return "critical"
	default:
		return "info"
	}
}

// Surface is the artifact an advisory was observed on: the project's SOURCE
// (manifests/lockfiles scanned before build) or the built IMAGE (a container
// scan in the review phase). One canonical advisory may be seen on either or
// both; recording the surface lets review reconcile image-scan observations
// against the source Assessment persisted by the audition.
type Surface string

const (
	SurfaceSource Surface = "source"
	SurfaceImage  Surface = "image"
)

// AdvisoryObservation is one raw per-source report that a package version is
// affected by an advisory. Several observations (from different sources, or the
// same advisory under different IDs) may describe one real vulnerability;
// canonicalize collapses them. Severity is normalized to the OSV label
// vocabulary ("CRITICAL"|"HIGH"|"MODERATE"|"LOW"|"UNKNOWN") at collection time so
// heterogeneous sources (a CVSS label vs. a numeric score) compare on one scale.
type AdvisoryObservation struct {
	Source    string // "osv-api" | "osv-scanner"
	VulnID    string
	Aliases   []string
	Package   string
	Version   string // affected package version, for triage in the rendered message
	Ecosystem string
	Severity  string // normalized OSV severity label
	FixedIn   string
	Summary   string
	File      string // repo-relative manifest/lockfile, for finding attribution
	Line      int
	Surface   Surface // which surface this observation came from (source vs image)
}

// Vulnerability is one canonical advisory: the union of every observation that
// describes it. It carries the highest severity seen, a representative
// summary/fixed-in, and the set of affected packages (each "name@version" when a
// version is known). Verdict is assigned by evaluate. File/Line attribute the
// single rendered finding to a representative source location.
type Vulnerability struct {
	ID       string
	Aliases  []string
	Severity string
	FixedIn  string
	Summary  string
	Packages []string
	File     string
	Line     int
	Verdict  Verdict

	// Ecosystem is the representative ecosystem for this advisory, used to route
	// evidence contributors (e.g. "gomod" → the Go reachability analyzer). Chosen
	// deterministically by mergeComponent.
	Ecosystem string
	// Surfaces is the DISTINCT surfaces this advisory was observed on, sorted and
	// deduped — [source], [image], or [image source]. Aggregated by mergeComponent
	// from the component's observations; an observation with an empty Surface
	// contributes nothing.
	Surfaces []Surface
	// Evidence holds enrichment facts (reachability today; KEV/EPSS/fix later)
	// attached by Assess. Reduce attaches none, so it stays nil there.
	Evidence []evidence.Evidence
}

// Reduce composes canonicalize → evaluate: it groups observations into one
// Vulnerability per advisory and assigns each a Verdict. Pure and deterministic —
// the same observations always produce the same vulnerabilities in the same order.
func Reduce(obs []AdvisoryObservation) []Vulnerability {
	vulns := canonicalize(obs)
	for i := range vulns {
		vulns[i].Verdict = evaluate(vulns[i])
	}
	return vulns
}
