// Package modules contains all built-in lint modules.
// Import this package to register all modules via their init() functions.
package modules

import (
	// Register the freshness sub-package module.
	_ "github.com/PrPlanIT/StageFreight/src/lint/modules/freshness"
	// Register the vulnerabilities sub-package module (canonical CVE renderer).
	_ "github.com/PrPlanIT/StageFreight/src/lint/modules/vulnerabilities"
)
