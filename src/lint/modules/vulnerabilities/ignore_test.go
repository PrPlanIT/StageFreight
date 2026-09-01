package vulnerabilities

import (
	"github.com/PrPlanIT/StageFreight/src/supplychain"
	"github.com/PrPlanIT/StageFreight/src/supplychain/analysis"
	"testing"
)

// The gate blocks on advisories, so the operator's exceptions have to reach it — an
// exception the gate cannot see leaves a finding demanding a fix that was already
// consciously declined, and no rerun can ever clear it.
func TestDropIgnored(t *testing.T) {
	obs := []analysis.AdvisoryObservation{
		{VulnID: "GHSA-accepted"},
		{VulnID: "GHSA-other"},
		{VulnID: "OSV-1", Aliases: []string{"CVE-2026-9999"}},
	}

	m := &vulnModule{ignores: []supplychain.VulnIgnore{
		{ID: "ghsa-accepted", Reason: "not in the shipped image"},
		{ID: "CVE-2026-9999", Reason: "matched through the alias, not the primary id"},
	}}
	got := m.dropIgnored(obs)
	if len(got) != 1 || got[0].VulnID != "GHSA-other" {
		t.Fatalf("kept %+v, want only GHSA-other", got)
	}

	// A lapsed exception must report again: the acceptance expired, the risk did not.
	m.ignores = []supplychain.VulnIgnore{{ID: "GHSA-accepted", Until: "2000-01-01"}}
	if len(m.dropIgnored(obs)) != len(obs) {
		t.Error("a lapsed exception must stop suppressing")
	}

	// No exceptions declared: nothing is dropped, and the input is not aliased away.
	m.ignores = nil
	if len(m.dropIgnored(obs)) != len(obs) {
		t.Error("with no exceptions every advisory must report")
	}
}
