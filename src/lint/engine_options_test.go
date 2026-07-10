package lint

import (
	"reflect"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// TestModuleOptionsVulnerabilitiesFallsBackToFreshness preserves the existing
// behavior: with no options under the vulnerabilities/osv key, the
// vulnerabilities module still reads the shared vulnerability config that
// lives under lint.modules.freshness.options.
func TestModuleOptionsVulnerabilitiesFallsBackToFreshness(t *testing.T) {
	cfg := config.LintConfig{
		Modules: map[string]config.ModuleConfig{
			"freshness": {Options: map[string]any{"min_severity": "high"}},
		},
	}
	got := moduleOptions(cfg, "vulnerabilities")
	want := map[string]any{"min_severity": "high"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moduleOptions(vulnerabilities) = %v, want %v (fallback to freshness)", got, want)
	}
}

// TestModuleOptionsVulnerabilitiesOwnKeyHonored is the fix under test (LOW #2):
// options placed under the module's own canonical key
// (lint.modules.vulnerabilities.options) must be honored, not silently
// dropped in favor of freshness.
func TestModuleOptionsVulnerabilitiesOwnKeyHonored(t *testing.T) {
	cfg := config.LintConfig{
		Modules: map[string]config.ModuleConfig{
			"freshness":       {Options: map[string]any{"min_severity": "low"}},
			"vulnerabilities": {Options: map[string]any{"min_severity": "critical"}},
		},
	}
	got := moduleOptions(cfg, "vulnerabilities")
	want := map[string]any{"min_severity": "critical"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moduleOptions(vulnerabilities) = %v, want %v (own key must win over freshness)", got, want)
	}
}

// TestModuleOptionsVulnerabilitiesOSVAliasHonored: the deprecated "osv" key is
// the module's own alias, so options placed there must also take precedence
// over freshness, exactly like the canonical "vulnerabilities" key.
func TestModuleOptionsVulnerabilitiesOSVAliasHonored(t *testing.T) {
	cfg := config.LintConfig{
		Modules: map[string]config.ModuleConfig{
			"freshness": {Options: map[string]any{"min_severity": "low"}},
			"osv":       {Options: map[string]any{"min_severity": "critical"}},
		},
	}
	got := moduleOptions(cfg, "vulnerabilities")
	want := map[string]any{"min_severity": "critical"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moduleOptions(vulnerabilities) = %v, want %v (osv alias must win over freshness)", got, want)
	}
}

// TestModuleOptionsVulnerabilitiesNoOptionsAnywhere: neither section configured
// returns nil (module applies its defaults) rather than panicking.
func TestModuleOptionsVulnerabilitiesNoOptionsAnywhere(t *testing.T) {
	cfg := config.LintConfig{Modules: map[string]config.ModuleConfig{}}
	if got := moduleOptions(cfg, "vulnerabilities"); got != nil {
		t.Errorf("moduleOptions(vulnerabilities) = %v, want nil", got)
	}
}
