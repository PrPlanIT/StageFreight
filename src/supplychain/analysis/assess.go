package analysis

import (
	"context"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// Assess is Reduce plus evidence enrichment: canonicalize (pure) -> enrich via
// contributors (I/O) -> evaluate (pure, now evidence-aware). Callers with no
// registry use Reduce; Assess is for the audition path that runs govulncheck.
func Assess(ctx context.Context, obs []AdvisoryObservation, target evidence.Target, reg *evidence.Registry) []Vulnerability {
	vulns := canonicalize(obs)
	if reg == nil {
		for i := range vulns {
			vulns[i].Verdict = evaluate(vulns[i])
		}
		return vulns
	}
	evulns := make([]evidence.Vulnerability, len(vulns))
	for i, v := range vulns {
		evulns[i] = evidence.Vulnerability{
			ID: v.ID, Aliases: v.Aliases, Ecosystem: normalizeEcosystem(v.Ecosystem),
			Package: firstPackageName(v.Packages), Severity: v.Severity,
		}
	}
	findings := reg.Enrich(ctx, target, evulns)
	// Enrich returns findings in the SAME order as the input evulns slice (it
	// ranges `for _, v := range vulns`), so findings[i] aligns with vulns[i] by
	// advisory ID. This index-alignment invariant must hold — do not reorder.
	for i := range vulns {
		if i < len(findings) {
			vulns[i].Evidence = findings[i].Evidence
		}
		vulns[i].Verdict = evaluate(vulns[i])
	}
	return vulns
}

// normalizeEcosystem maps the discovery ecosystem vocabulary to the evidence
// contributor vocabulary: "gomod" is what discovery records for Go modules, but
// contributors key on "go". Everything else (npm, cargo, pypi, …) passes through
// unchanged. Lowercase-trimmed first.
func normalizeEcosystem(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if e == "gomod" {
		return "go"
	}
	return e
}

// firstPackageName returns the first affected package name with any "@version"
// suffix stripped ("golang.org/x/crypto@v0.54.0" -> "golang.org/x/crypto"); "" if
// there are none. This is the reachability join key the contributor expects.
func firstPackageName(pkgs []string) string {
	if len(pkgs) == 0 {
		return ""
	}
	name := pkgs[0]
	if at := strings.LastIndex(name, "@"); at >= 0 {
		name = name[:at]
	}
	return name
}
