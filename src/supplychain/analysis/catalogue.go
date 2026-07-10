package analysis

import (
	"encoding/json"

	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis/evidence"
)

// VulnRecord is the serialization-clean form of a canonical Vulnerability for
// the cross-phase catalogue artifact. Flat, JSON-stable — no Go interfaces.
type VulnRecord struct {
	ID           string              `json:"id"`
	Aliases      []string            `json:"aliases,omitempty"`
	Severity     string              `json:"severity,omitempty"`
	Verdict      string              `json:"verdict"` // v.Verdict.String()
	Packages     []string            `json:"packages,omitempty"`
	File         string              `json:"file,omitempty"`
	Line         int                 `json:"line,omitempty"`
	Ecosystem    string              `json:"ecosystem,omitempty"`
	Surfaces     []string            `json:"surfaces,omitempty"` // Surface values as strings
	Reachability *ReachabilityRecord `json:"reachability,omitempty"`
}

// ReachabilityRecord is the flattened form of an evidence.ReachabilityEvidence,
// carrying its enum states as their String() forms so it round-trips cleanly.
type ReachabilityRecord struct {
	State      string   `json:"state"`                // ReachabilityState.String(): "reachable"|"unreachable"|"unknown"
	Analyzer   string   `json:"analyzer,omitempty"`   //
	Confidence string   `json:"confidence,omitempty"` // Confidence.String()
	Facts      []string `json:"facts,omitempty"`      //
}

// SourceAssessment is the cross-phase catalogue artifact: the source-side
// Assessment the audition persists so the review phase can read it back and
// reconcile image-scan observations against it.
type SourceAssessment struct {
	Vulnerabilities []VulnRecord `json:"vulnerabilities"`
}

// ToRecord converts a canonical Vulnerability to its serialization-clean form,
// flattening any attached reachability evidence.
func ToRecord(v Vulnerability) VulnRecord {
	rec := VulnRecord{
		ID:        v.ID,
		Aliases:   v.Aliases,
		Severity:  v.Severity,
		Verdict:   v.Verdict.String(),
		Packages:  v.Packages,
		File:      v.File,
		Line:      v.Line,
		Ecosystem: v.Ecosystem,
	}
	if len(v.Surfaces) > 0 {
		rec.Surfaces = make([]string, len(v.Surfaces))
		for i, s := range v.Surfaces {
			rec.Surfaces[i] = string(s)
		}
	}
	for _, e := range v.Evidence {
		if r, ok := e.(evidence.ReachabilityEvidence); ok {
			rec.Reachability = &ReachabilityRecord{
				State:      r.State.String(),
				Analyzer:   r.Analyzer,
				Confidence: r.Confidence.String(),
				Facts:      r.Facts,
			}
			break
		}
	}
	return rec
}

// MarshalSourceAssessment renders the vulnerabilities as the indented JSON
// catalogue artifact (2-space indent).
func MarshalSourceAssessment(vulns []Vulnerability) ([]byte, error) {
	records := make([]VulnRecord, len(vulns))
	for i, v := range vulns {
		records[i] = ToRecord(v)
	}
	return json.MarshalIndent(SourceAssessment{Vulnerabilities: records}, "", "  ")
}

// UnmarshalSourceAssessment parses the catalogue artifact back into a
// SourceAssessment.
func UnmarshalSourceAssessment(data []byte) (*SourceAssessment, error) {
	var sa SourceAssessment
	if err := json.Unmarshal(data, &sa); err != nil {
		return nil, err
	}
	return &sa, nil
}
