package build

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// With no builder registered EnsureBuildxBuilder errors; once registered, it answers.
func TestEnsureBuildxBuilder(t *testing.T) {
	RegisterBuildxBuilder(nil)
	t.Cleanup(func() { RegisterBuildxBuilder(nil) })
	if _, err := EnsureBuildxBuilder(config.BuilderConfig{}); err == nil || !strings.Contains(err.Error(), "no buildx builder") {
		t.Fatalf("err = %v, want no builder registered", err)
	}
	RegisterBuildxBuilder(func(cfg config.BuilderConfig) (string, error) { return cfg.BuilderName(), nil })
	if name, err := EnsureBuildxBuilder(config.BuilderConfig{Name: "mine"}); err != nil || name != "mine" {
		t.Fatalf("EnsureBuildxBuilder = %q, %v; want mine", name, err)
	}
}
