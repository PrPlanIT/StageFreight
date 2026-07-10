package analysis

import (
	"context"
	"reflect"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// fakeReach is a stand-in reachability contributor (the real one shells govulncheck). It
// attaches a fixed ReachabilityEvidence.State to every vuln whose ID is in ids, keyed by
// v.Ref() as the framework requires.
type fakeReach struct {
	state evidence.ReachabilityState
	ids   map[string]bool
}

func (fakeReach) Name() string             { return "fake" }
func (fakeReach) Supports(e string) bool   { return e == "go" }
func (f fakeReach) Contribute(_ context.Context, _ evidence.Target, vulns []evidence.Vulnerability) (map[evidence.VulnRef]evidence.Evidence, error) {
	out := map[evidence.VulnRef]evidence.Evidence{}
	for _, v := range vulns {
		if f.ids[v.ID] {
			out[v.Ref()] = evidence.ReachabilityEvidence{State: f.state, Analyzer: "fake", Confidence: evidence.ConfidenceHigh}
		}
	}
	return out, nil
}

// gomodCritical is the CRITICAL Go-module advisory the reachability layer exists to re-judge.
func gomodCritical() []AdvisoryObservation {
	return []AdvisoryObservation{{
		Source:    "osv-api",
		VulnID:    "GO-x",
		Package:   "golang.org/x/crypto",
		Version:   "v0.54.0",
		Ecosystem: "gomod",
		Severity:  "CRITICAL",
		File:      "go.mod",
		Line:      1,
	}}
}

func assessOne(t *testing.T, state evidence.ReachabilityState, hit bool) Vulnerability {
	t.Helper()
	ids := map[string]bool{}
	if hit {
		ids["GO-x"] = true
	}
	reg := evidence.NewRegistry(fakeReach{state: state, ids: ids})
	vulns := Assess(context.Background(), gomodCritical(), evidence.Target{}, reg)
	if len(vulns) != 1 {
		t.Fatalf("want 1 canonical vuln, got %d", len(vulns))
	}
	return vulns[0]
}

// A proven-unreachable critical is downgraded to Info and carries the reachability evidence.
func TestAssess_UnreachableDowngradesToInfo(t *testing.T) {
	v := assessOne(t, evidence.ReachUnreachable, true)
	if v.Verdict != VerdictInfo {
		t.Fatalf("unreachable critical: verdict = %s, want info", v.Verdict)
	}
	if len(v.Evidence) == 0 {
		t.Fatal("expected reachability evidence attached")
	}
	r, ok := v.Evidence[0].(evidence.ReachabilityEvidence)
	if !ok || r.State != evidence.ReachUnreachable {
		t.Fatalf("expected unreachable evidence, got %+v (ok=%v)", v.Evidence[0], ok)
	}
}

// A proven-reachable critical keeps its severity verdict.
func TestAssess_ReachableKeepsCritical(t *testing.T) {
	v := assessOne(t, evidence.ReachReachable, true)
	if v.Verdict != VerdictCritical {
		t.Fatalf("reachable critical: verdict = %s, want critical", v.Verdict)
	}
}

// No evidence for the vuln (Unknown / absent) → fail-closed, no downgrade.
func TestAssess_UnknownIsFailClosed(t *testing.T) {
	v := assessOne(t, evidence.ReachUnreachable, false) // contributor returns nothing for GO-x
	if v.Verdict != VerdictCritical {
		t.Fatalf("no evidence: verdict = %s, want critical (fail-closed)", v.Verdict)
	}
	if len(v.Evidence) != 0 {
		t.Fatalf("expected no evidence attached, got %+v", v.Evidence)
	}
}

// Assess with a nil registry is identical to Reduce (verdict from severity only).
func TestAssess_NilRegistryMatchesReduce(t *testing.T) {
	obs := gomodCritical()
	got := Assess(context.Background(), obs, evidence.Target{}, nil)
	want := Reduce(obs)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Assess(nil reg) = %+v, want Reduce = %+v", got, want)
	}
}

func TestNormalizeEcosystem(t *testing.T) {
	if got := normalizeEcosystem("gomod"); got != "go" {
		t.Fatalf("normalizeEcosystem(gomod) = %q, want go", got)
	}
	if got := normalizeEcosystem("NPM"); got != "npm" {
		t.Fatalf("normalizeEcosystem(NPM) = %q, want npm", got)
	}
}

func TestFirstPackageName(t *testing.T) {
	if got := firstPackageName([]string{"golang.org/x/crypto@v0.54.0"}); got != "golang.org/x/crypto" {
		t.Fatalf("firstPackageName = %q, want golang.org/x/crypto", got)
	}
	if got := firstPackageName(nil); got != "" {
		t.Fatalf("firstPackageName(nil) = %q, want empty", got)
	}
}
