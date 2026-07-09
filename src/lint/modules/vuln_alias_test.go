package modules

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/lint"
)

// In package modules so the vulnerabilities module's init() registration is
// available to NewEngine.

func hasModule(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestOSVAliasExplicitSelection (fix #3): `--module osv` resolves to the renamed
// vulnerabilities module instead of failing with "unknown module".
func TestOSVAliasExplicitSelection(t *testing.T) {
	cfg := config.DefaultLintConfig()
	eng, err := lint.NewEngine(cfg, t.TempDir(), []string{"osv"}, nil, false, nil)
	if err != nil {
		t.Fatalf("NewEngine([osv]) errored: %v", err)
	}
	if !hasModule(eng.ModuleNames(), "vulnerabilities") {
		t.Errorf("modules = %v, want to contain vulnerabilities (osv alias)", eng.ModuleNames())
	}
}

// TestOSVAliasExplicitDedup (fix #3): an explicit list naming both the old and new
// names selects the module exactly once, not twice (which would double-report).
func TestOSVAliasExplicitDedup(t *testing.T) {
	cfg := config.DefaultLintConfig()
	eng, err := lint.NewEngine(cfg, t.TempDir(), []string{"osv", "vulnerabilities"}, nil, false, nil)
	if err != nil {
		t.Fatalf("NewEngine errored: %v", err)
	}
	count := 0
	for _, n := range eng.ModuleNames() {
		if n == "vulnerabilities" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("vulnerabilities selected %d times, want 1", count)
	}
}

// TestOSVAliasConfigDisable (fix #3): lint.modules.osv.enabled=false disables the
// vulnerabilities module, preserving the deprecated config key's effect.
func TestOSVAliasConfigDisable(t *testing.T) {
	disabled := false
	cfg := config.DefaultLintConfig()
	cfg.Modules = map[string]config.ModuleConfig{
		"osv": {Enabled: &disabled},
	}
	eng, err := lint.NewEngine(cfg, t.TempDir(), nil, nil, false, nil)
	if err != nil {
		t.Fatalf("NewEngine errored: %v", err)
	}
	if hasModule(eng.ModuleNames(), "vulnerabilities") {
		t.Errorf("modules = %v, want vulnerabilities DISABLED via osv key", eng.ModuleNames())
	}
}

// TestOSVAliasNoModuleSkip (fix #3): `--no-module osv` skips the vulnerabilities
// module.
func TestOSVAliasNoModuleSkip(t *testing.T) {
	cfg := config.DefaultLintConfig()
	eng, err := lint.NewEngine(cfg, t.TempDir(), nil, []string{"osv"}, false, nil)
	if err != nil {
		t.Fatalf("NewEngine errored: %v", err)
	}
	if hasModule(eng.ModuleNames(), "vulnerabilities") {
		t.Errorf("modules = %v, want vulnerabilities skipped via --no-module osv", eng.ModuleNames())
	}
}
