// Package auditionproof is the evidence model for audition: audition produces
// proof results, later phases consume them. It is intentionally bigger than any
// one proof — today it carries GitOps (Flux) validation; freshness, eligibility,
// crucible, etc. are typed fields added as they adopt the model. This package is
// the *evidence* layer only (what was proven), NOT enforcement (required vs
// advisory gating), which is a separate, deferred design.
//
// The results are persisted to the `.stagefreight/` handoff that audition uploads
// as a CI artifact and perform downloads — so perform acts on the exact evidence
// the operator reviewed in audition, never a recomputed second verdict.
package auditionproof

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/atomicfile"
)

// ResultsPath is the workspace-relative path where audition proof results live.
const ResultsPath = ".stagefreight/proof-results.json"

// Results is the per-run set of audition proof outcomes. Each proof is a typed,
// omit-empty field so the JSON grows additively as proofs adopt the model.
type Results struct {
	Version      int           `json:"version"`
	FluxValidate *FluxValidate `json:"flux_validate,omitempty"`
}

// FluxValidate is the GitOps validation proof: a verdict per Flux Kustomization
// (keyed "namespace/name" — the unit of truth), plus coverage facts.
type FluxValidate struct {
	Roots    int                `json:"roots"`
	Skipped  string             `json:"skipped,omitempty"` // validation could not run (e.g. tool unavailable)
	Verdicts map[string]Verdict `json:"verdicts,omitempty"`
	NoSchema map[string]int     `json:"no_schema,omitempty"`
}

// Verdict is one Kustomization's outcome. Status is "pass" | "warn" | "fail".
type Verdict struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons,omitempty"`
}

// Read returns the proof results from the workspace. A missing file is not an
// error — it yields an empty Results (the proof simply did not run / persist).
func Read(rootDir string) (*Results, error) {
	p := filepath.Join(rootDir, ResultsPath)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &Results{Version: 1}, nil
		}
		return nil, fmt.Errorf("reading proof results: %w", err)
	}
	var r Results
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing proof results: %w", err)
	}
	return &r, nil
}

// Write persists the proof results atomically (tmp + fsync + rename).
func Write(rootDir string, r *Results) error {
	r.Version = 1
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling proof results: %w", err)
	}
	data = append(data, '\n')
	return atomicfile.WriteFile(filepath.Join(rootDir, ResultsPath), data, 0o644)
}
