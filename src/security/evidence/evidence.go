// Package evidence is StageFreight's security-ENRICHMENT layer. The scanners (OSV, Trivy,
// Grype) DISCOVER vulnerabilities; this package ENRICHES each discovered vulnerability with
// evidence so policy can judge the full story — "Critical + reachable + KEV-listed + fixable"
// — instead of a bare severity.
//
// The pipeline is:
//
//	Discovery (scanners) → Normalized Vulnerabilities → Enrichment (contributors) →
//	Findings (vuln + evidence) → Policy → Renderer
//
// Reachability (govulncheck for Go) is deliberately just the FIRST evidence contributor, not
// the framework itself. KEV, EPSS, fix-availability, SBOM-ownership, exploit-maturity and
// runtime-loaded evidence all slot in behind the same EvidenceContributor seam without
// touching the pipeline. Evidence is DATA — a contributor never decides policy.
package evidence

import "context"

// Vulnerability is a normalized finding from the discovery layer — the join identity every
// contributor enriches. Kept minimal on purpose: each scanner maps its native output into this.
//
// ID is the identifier the discovering scanner used, but it is NOT the correlation contract:
// the same underlying vulnerability carries several identifiers (CVE, GHSA, GO advisory), and
// a contributor may report under a different one than discovery did. Aliases holds the other
// known identifiers (OSV publishes them), and contributors correlate against Identifiers() —
// the full set — never a single scanner-specific ID.
type Vulnerability struct {
	ID        string   // the discovering scanner's identifier, e.g. "GO-2026-5932", "CVE-2026-1234"
	Aliases   []string // other identifiers for the same advisory, e.g. ["CVE-…", "GHSA-…"]
	Ecosystem string   // normalized: "go", "rust", "npm", "oci", …
	Package   string   // AFFECTED package (the reachability join key), e.g. "golang.org/x/crypto/openpgp"
	Symbol    string   // optional affected symbol
	Severity  string   // "CRITICAL" | "HIGH" | "MODERATE" | "LOW" | "" (as the scanner reported)
	Source    string   // which scanner discovered it: "osv" | "trivy" | "grype"
}

// Identifiers returns every identifier this vulnerability is known by — its primary ID plus any
// aliases. Contributors correlate their findings against this set so a CVE-keyed discovery still
// joins a GO-advisory-keyed analyzer (and vice versa), without OSV-ID ever being the contract.
func (v Vulnerability) Identifiers() []string {
	if len(v.Aliases) == 0 {
		return []string{v.ID}
	}
	ids := make([]string, 0, 1+len(v.Aliases))
	ids = append(ids, v.ID)
	ids = append(ids, v.Aliases...)
	return ids
}

// VulnRef is the stable identity used to index evidence back onto a vulnerability.
type VulnRef struct {
	ID        string
	Ecosystem string
	Package   string
	Symbol    string
}

// Ref is the join key for a vulnerability.
func (v Vulnerability) Ref() VulnRef {
	return VulnRef{ID: v.ID, Ecosystem: v.Ecosystem, Package: v.Package, Symbol: v.Symbol}
}

// Evidence is one enrichment fact about a vulnerability. It is DATA, never a verdict — policy
// alone interprets it. Each concrete kind (ReachabilityEvidence today; KEVEvidence,
// EPSSEvidence, FixEvidence… later) implements this.
type Evidence interface {
	Kind() string // "reachability", "kev", "epss", …
}

// Finding is a discovered vulnerability plus the evidence contributors have attached.
type Finding struct {
	Vuln     Vulnerability
	Evidence []Evidence
}

// Target carries what contributors need to do their work — e.g. a reachability contributor
// needs the source root (call-graph analysis needs source + a build). Extensible without
// changing the interface.
type Target struct {
	// EcosystemDir maps a normalized ecosystem to the module/source root its contributors
	// should examine. A KEV/EPSS contributor ignores it; a reachability contributor needs it.
	EcosystemDir map[string]string
}

// EvidenceContributor enriches findings for the ecosystems it supports. It is the pluggable
// seam: a Go reachability contributor (govulncheck), a Rust one (experimental), and
// ecosystem-agnostic ones (KEV, EPSS) all satisfy it. A contributor produces evidence; it never
// decides policy, and it must never fabricate evidence for something it did not examine.
type EvidenceContributor interface {
	Name() string
	Supports(ecosystem string) bool
	// Contribute returns evidence to attach, indexed by the vulnerability it enriches. A vuln
	// absent from the result simply gets no evidence from this contributor.
	Contribute(ctx context.Context, target Target, vulns []Vulnerability) (map[VulnRef]Evidence, error)
}

// Registry holds the enabled contributors, run in registration order.
type Registry struct{ contributors []EvidenceContributor }

// NewRegistry builds a registry from the given contributors.
func NewRegistry(cs ...EvidenceContributor) *Registry { return &Registry{contributors: cs} }

// Register appends a contributor.
func (r *Registry) Register(c EvidenceContributor) { r.contributors = append(r.contributors, c) }

// Enrich runs every contributor over the vulnerabilities it supports and returns Findings with
// evidence attached. It is fail-closed and monotonic: a missing contributor for an ecosystem,
// or a contributor that errors, simply adds NO evidence — nothing is downgraded on absent
// evidence. Enrichment can only ever ADD signal; absence leaves a finding at its scanner
// severity (see policy.go).
func (r *Registry) Enrich(ctx context.Context, target Target, vulns []Vulnerability) []Finding {
	byRef := make(map[VulnRef][]Evidence)
	for _, c := range r.contributors {
		var supported []Vulnerability
		for _, v := range vulns {
			if c.Supports(v.Ecosystem) {
				supported = append(supported, v)
			}
		}
		if len(supported) == 0 {
			continue
		}
		ev, err := c.Contribute(ctx, target, supported)
		if err != nil {
			continue // fail-closed: a failed contributor adds nothing, never a downgrade
		}
		for ref, e := range ev {
			byRef[ref] = append(byRef[ref], e)
		}
	}
	findings := make([]Finding, 0, len(vulns))
	for _, v := range vulns {
		findings = append(findings, Finding{Vuln: v, Evidence: byRef[v.Ref()]})
	}
	return findings
}
