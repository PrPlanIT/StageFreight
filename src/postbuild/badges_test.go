package postbuild

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
)

func makePC() *pipeline.PipelineContext {
	return &pipeline.PipelineContext{
		Ctx:           context.Background(),
		Config:        &config.Config{},
		Writer:        io.Discard,
		PipelineStart: time.Now(),
		Scratch:       make(map[string]any),
	}
}

func TestBadgeHook_ConditionFalseWhenNoNarrator(t *testing.T) {
	appCfg := &config.Config{} // empty narrator
	runner := func(w io.Writer, color bool, rootDir string) (string, time.Duration) {
		return "0 generated", 0
	}
	hook := BadgeHook(appCfg, runner)

	pc := makePC()
	if hook.Condition(pc) {
		t.Error("BadgeHook.Condition = true with empty narrator config; want false")
	}
}

func TestBadgeHook_RunCallsRunner(t *testing.T) {
	var runnerCalled bool

	appCfg := &config.Config{}
	runner := func(w io.Writer, color bool, rootDir string) (string, time.Duration) {
		runnerCalled = true
		return "3 badges", time.Millisecond
	}
	hook := BadgeHook(appCfg, runner)

	pc := makePC()

	result, err := hook.Run(pc)
	if err != nil {
		t.Fatalf("BadgeHook.Run() returned error: %v", err)
	}
	if !runnerCalled {
		t.Error("badge runner was not called")
	}
	if result.Status != "success" {
		t.Errorf("status = %q; want %q", result.Status, "success")
	}
}
