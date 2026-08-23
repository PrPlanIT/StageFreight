package pipeline

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/version"
)

// IdentityInfo names the TOOL: its version is the orchestrator binary's own ldflags
// (version.Version), and it deliberately ignores the CI environment. The repo's
// commit/branch belong to the ── Code ── block (CIContextKV, tested above), not the
// StageFreight identity line. Guards the 0fc1930 regression fix: even with CI_COMMIT_*
// set, the identity line carries no repo SHA or branch.
func TestIdentityInfo_IsToolOnly(t *testing.T) {
	t.Setenv("CI_COMMIT_SHORT_SHA", "deadbee")
	t.Setenv("CI_COMMIT_BRANCH", "feature/x")
	t.Setenv("CI_COMMIT_TAG", "v9.9.9")

	info := IdentityInfo()
	if info.Version != version.Version {
		t.Errorf("Version = %q, want orchestrator version.Version %q", info.Version, version.Version)
	}
	if info.SHA != "" {
		t.Errorf("SHA = %q, want empty — the repo SHA belongs to the Code block, not the tool identity", info.SHA)
	}
	if info.Branch != "" {
		t.Errorf("Branch = %q, want empty — the repo branch belongs to the Code block, not the tool identity", info.Branch)
	}
}
