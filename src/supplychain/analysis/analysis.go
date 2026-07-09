// Package analysis is the supply-chain vulnerability analysis layer. It takes
// raw per-source advisory reports (the OSV-API correlation already attached to a
// Snapshot's dependencies, plus a fresh osv-scanner run) and reduces them to ONE
// canonical vulnerability per advisory with ONE verdict, so a given advisory is
// reported exactly once regardless of how many sources observed it.
//
// The pipeline is collect → canonicalize → evaluate:
//
//   - collect   gathers AdvisoryObservations from every source (policy-free I/O).
//   - canonicalize groups observations that describe the SAME advisory (their
//     id-sets intersect) into one Vulnerability — pure, deterministic, policy-free.
//   - evaluate  assigns exactly one Verdict per Vulnerability from its severity.
//
// The resulting Assessment is immutable: produced once, shared read-only. Rendering
// to lint findings lives OUTSIDE this package (src/lint/modules/vulnerabilities) so
// analysis carries no dependency on the lint layer.
package analysis

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

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
	Ecosystem string
	Severity  string // normalized OSV severity label
	FixedIn   string
	Summary   string
	File      string // repo-relative manifest/lockfile, for finding attribution
	Line      int
}

// Vulnerability is one canonical advisory: the union of every observation that
// describes it. It carries the highest severity seen, a representative
// summary/fixed-in, and the set of affected packages. Verdict is assigned by
// evaluate. File/Line attribute the single rendered finding to a representative
// source location.
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
}

// Assessment is the immutable result of one analysis pass: the canonical set of
// vulnerabilities, each already carrying its verdict. Produced once.
type Assessment struct {
	Vulnerabilities []Vulnerability
}

// Config carries the inputs collection needs beyond the Snapshot. analysis is
// deliberately free of the provisioning/lint layers (so lint may depend on it
// without a cycle): the caller resolves the osv-scanner binary and hands its path
// and a clean environment in. An empty ScannerBinPath skips the osv-scanner
// source entirely.
type Config struct {
	RootDir        string
	ScannerBinPath string   // resolved osv-scanner binary; "" → skip osv-scanner
	ScannerEnv     []string // environment for the osv-scanner process
}

// Analyze composes collect → canonicalize → evaluate into an immutable
// Assessment. snapshot supplies the OSV-API observations (already correlated onto
// its dependencies); cfg drives the osv-scanner run. The returned error is
// non-nil only for a genuine collection failure (e.g. a pinned osv-scanner
// version that will not resolve, or a scanner crash); the Assessment is still
// returned with whatever observations were gathered, so a scanner problem never
// discards the OSV-API findings.
func Analyze(ctx context.Context, snapshot *supplychain.Snapshot, cfg Config) (*Assessment, error) {
	obs, err := collectObservations(ctx, snapshot, cfg)
	vulns := canonicalize(obs)
	for i := range vulns {
		vulns[i].Verdict = evaluate(vulns[i])
	}
	return &Assessment{Vulnerabilities: vulns}, err
}
