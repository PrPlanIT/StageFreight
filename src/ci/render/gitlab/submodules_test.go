package gitlab

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/ci/render/model"
)

// A pipeline that clones without submodules leaves the directory empty, and the build
// fails on a missing file rather than on anything to do with the checkout — so the
// variable has to be emitted for a repo that declares them.
func TestSubmoduleStrategyIsEmittedWhenDeclared(t *testing.T) {
	out := emitJobFor(t, model.SourceSpec{FullClone: true, Submodules: true})
	if !strings.Contains(out, "GIT_SUBMODULE_STRATEGY: recursive") {
		t.Errorf("submodule strategy missing:\n%s", out)
	}
	// recursive, not normal: a submodule with its own submodules is still a missing
	// directory to whatever needs it.
	if strings.Contains(out, "GIT_SUBMODULE_STRATEGY: normal") {
		t.Error("emitted a non-recursive strategy")
	}
}

// A repo without submodules must not pay for the fetch, and its rendered pipeline must
// not gain a variable it has no use for.
func TestSubmoduleStrategyAbsentByDefault(t *testing.T) {
	out := emitJobFor(t, model.SourceSpec{FullClone: true})
	if strings.Contains(out, "GIT_SUBMODULE_STRATEGY") {
		t.Errorf("emitted a submodule strategy for a repo that declared none:\n%s", out)
	}
	if !strings.Contains(out, "GIT_DEPTH: 0") {
		t.Error("full clone should still emit GIT_DEPTH")
	}
}

// emitJobFor renders a minimal one-job pipeline with the given source spec.
func emitJobFor(t *testing.T, src model.SourceSpec) string {
	t.Helper()
	out, err := Emit(model.Pipeline{
		Jobs: []model.Job{{
			Name:     "perform",
			Stage:    "perform",
			Commands: []string{"stagefreight ci run perform"},
			Source:   src,
		}},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	return string(out)
}
