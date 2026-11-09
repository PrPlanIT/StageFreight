package cmd

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// osvScannerResolver resolves osv-scanner for the dependency updater the way the lint
// does: the operator's toolchain pin when set, recorded in the run's staged tools.
func osvScannerResolver(rootDir string, want map[string]config.ToolConstraint) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		sel, selErr := toolchain.Select(rootDir, "osv-scanner", want)
		res, err := provision.ResolveSelection(ctx, rootDir, "osv-scanner", sel, selErr, "transitive vulnerability remediation")
		if err != nil {
			return "", err
		}
		return res.Path, nil
	}
}
