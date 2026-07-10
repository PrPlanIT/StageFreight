package analysis

import (
	"reflect"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// TestSourceAssessmentRoundTrip: a set of canonical vulnerabilities — one with
// reachability evidence attached, one without — survives MarshalSourceAssessment
// → UnmarshalSourceAssessment with its identity fields intact, and the
// interface-typed reachability evidence flattens into a serializable record that
// round-trips (present+state+fact for the enriched one, nil for the bare one).
func TestSourceAssessmentRoundTrip(t *testing.T) {
	vulns := []Vulnerability{
		{
			ID:        "GHSA-reach",
			Aliases:   []string{"CVE-2026-1"},
			Severity:  "CRITICAL",
			Verdict:   VerdictInfo, // proven-unreachable downgrades to info
			Packages:  []string{"golang.org/x/crypto@v0.54.0"},
			File:      "go.mod",
			Line:      3,
			Ecosystem: "gomod",
			Surfaces:  []Surface{SurfaceSource},
			Evidence: []evidence.Evidence{
				evidence.ReachabilityEvidence{
					State:      evidence.ReachUnreachable,
					Analyzer:   "govulncheck",
					Confidence: evidence.ConfidenceHigh,
					Facts:      []string{"no call path to golang.org/x/crypto/openpgp"},
				},
			},
		},
		{
			ID:        "GHSA-bare",
			Severity:  "HIGH",
			Verdict:   VerdictCritical,
			Packages:  []string{"lodash@4.17.20"},
			File:      "package.json",
			Line:      12,
			Ecosystem: "npm",
			Surfaces:  []Surface{SurfaceImage, SurfaceSource},
		},
	}

	data, err := MarshalSourceAssessment(vulns)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sa, err := UnmarshalSourceAssessment(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sa.Vulnerabilities) != 2 {
		t.Fatalf("want 2 records, got %d", len(sa.Vulnerabilities))
	}

	reach := sa.Vulnerabilities[0]
	if reach.ID != "GHSA-reach" {
		t.Errorf("record[0] ID = %q, want GHSA-reach", reach.ID)
	}
	if reach.Verdict != "info" {
		t.Errorf("record[0] Verdict = %q, want info", reach.Verdict)
	}
	if !reflect.DeepEqual(reach.Surfaces, []string{"source"}) {
		t.Errorf("record[0] Surfaces = %v, want [source]", reach.Surfaces)
	}
	if reach.Reachability == nil {
		t.Fatal("record[0] expected non-nil Reachability")
	}
	if reach.Reachability.State != "unreachable" {
		t.Errorf("record[0] Reachability.State = %q, want unreachable", reach.Reachability.State)
	}
	if reach.Reachability.Confidence != "high" {
		t.Errorf("record[0] Reachability.Confidence = %q, want high", reach.Reachability.Confidence)
	}
	if !reflect.DeepEqual(reach.Reachability.Facts, []string{"no call path to golang.org/x/crypto/openpgp"}) {
		t.Errorf("record[0] Reachability.Facts = %v, want the openpgp fact", reach.Reachability.Facts)
	}

	bare := sa.Vulnerabilities[1]
	if bare.ID != "GHSA-bare" {
		t.Errorf("record[1] ID = %q, want GHSA-bare", bare.ID)
	}
	if bare.Verdict != "critical" {
		t.Errorf("record[1] Verdict = %q, want critical", bare.Verdict)
	}
	if !reflect.DeepEqual(bare.Surfaces, []string{"image", "source"}) {
		t.Errorf("record[1] Surfaces = %v, want [image source]", bare.Surfaces)
	}
	if bare.Reachability != nil {
		t.Errorf("record[1] expected nil Reachability, got %+v", bare.Reachability)
	}
}
