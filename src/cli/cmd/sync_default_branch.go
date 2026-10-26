package cmd

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// projectDefaultBranch sets a mirror's default branch to its declared one when it differs,
// returning the report icon and detail. A forge that cannot set it, or a branch the mirror
// does not have yet, is reported rather than failing the sync.
func projectDefaultBranch(ctx context.Context, m config.ResolvedRepo, want string) (icon, detail string) {
	fc, err := forge.NewFromAccessory(m.Provider, m.BaseURL, m.Project, m.Credentials)
	if err != nil {
		return "failed", err.Error()
	}
	have, err := fc.DefaultBranch(ctx)
	if err == nil && have == want {
		return "success", want
	}
	setter, ok := fc.(forge.DefaultBranchSetter)
	if !ok {
		return "skipped", fmt.Sprintf("%s cannot set a default branch (declared %s)", m.Provider, want)
	}
	if err := setter.SetDefaultBranch(ctx, want); err != nil {
		return "warning", fmt.Sprintf("could not set %s (is it pushed?): %v", want, err)
	}
	return "success", fmt.Sprintf("%s → %s", have, want)
}
