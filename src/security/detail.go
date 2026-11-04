package security

import (
	"github.com/PrPlanIT/StageFreight/src/config"
)

// ResolveDetailLevel evaluates the security detail rules against the current
// tag and branch to determine which detail level to use in release notes.
// CLI override (if non-empty) takes precedence over all rules.
func ResolveDetailLevel(cfg config.SecurityConfig, cliOverride string) string {
	if cliOverride != "" {
		return cliOverride
	}

	tag, branch := config.CITag(), config.CIBranch()

	for _, rule := range cfg.ReleaseDetailRules {
		if config.MatchConditionWith(rule.Condition, tag, branch) {
			return rule.Detail
		}
	}

	if cfg.ReleaseDetail != "" {
		return cfg.ReleaseDetail
	}
	return "detailed"
}
