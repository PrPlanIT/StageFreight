package cmd

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/cistate"
)

// validateRunner (the governance/gitops audition path) records an audition contract from
// deriveAuditionContract so the pipeline badge reflects the REAL validation outcome — a
// clean run is passing, a failed run is FAILING (the earlier "unknown → passing" hack
// would have shown passing on a broken pipeline). These lock the mapping the fix relies on:
// the contract feeds PipelineStatus, which the pipeline badge renders via BUILD_STATUS.
func TestValidateAuditionContract_DrivesBadgeStatus(t *testing.T) {
	cases := []struct {
		name    string
		healthy bool
		err     bool
		want    string
	}{
		{"clean validation → passing", true, false, "passing"},
		{"failed validation → failing", true, true, "failing"},
		{"unhealthy runner → failing", false, true, "failing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			contract := deriveAuditionContract(auditionInputs{
				RunnerHealthy: c.healthy,
				Fatal:         c.healthy && c.err,
				TestsPassed:   !c.err,
			})
			if !contract.Attempted {
				t.Fatal("audition contract must be Attempted so PipelineStatus counts it (else it stays \"unknown\")")
			}
			st := &cistate.State{}
			st.RecordSubsystem(contract)
			if got := st.PipelineStatus(); got != c.want {
				t.Errorf("PipelineStatus = %q, want %q", got, c.want)
			}
		})
	}
}
