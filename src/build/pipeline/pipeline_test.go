package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
)

// makePC returns a minimal PipelineContext suitable for unit tests.
func makePC() *PipelineContext {
	return &PipelineContext{
		Ctx:           context.Background(),
		Config:        &config.Config{},
		Writer:        io.Discard,
		PipelineStart: time.Now(),
		Scratch:       make(map[string]any),
	}
}

// stubPhase returns a Phase that records whether it was called and optionally returns an error.
func stubPhase(name string, called *bool, returnErr error) Phase {
	return Phase{
		Name: name,
		Run: func(pc *PipelineContext) (*PhaseResult, error) {
			if called != nil {
				*called = true
			}
			if returnErr != nil {
				return nil, returnErr
			}
			return &PhaseResult{Name: name, Status: "success", Summary: name + " ok"}, nil
		},
	}
}

// --- Pipeline.Run ---

func TestPipeline_RunAllPhasesInOrder(t *testing.T) {
	var order []string
	makeTracking := func(name string) Phase {
		return Phase{
			Name: name,
			Run: func(pc *PipelineContext) (*PhaseResult, error) {
				order = append(order, name)
				return &PhaseResult{Name: name, Status: "success"}, nil
			},
		}
	}

	p := &Pipeline{
		Phases: []Phase{
			makeTracking("a"),
			makeTracking("b"),
			makeTracking("c"),
		},
	}
	if err := p.Run(makePC()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	want := []string{"a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("phase order = %v; want %v", order, want)
	}
	for i, w := range want {
		if order[i] != w {
			t.Errorf("phase[%d] = %q; want %q", i, order[i], w)
		}
	}
}

func TestPipeline_StopsOnPhaseError(t *testing.T) {
	var secondCalled bool

	p := &Pipeline{
		Phases: []Phase{
			{
				Name: "fail",
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					return nil, errors.New("phase failed")
				},
			},
			stubPhase("second", &secondCalled, nil),
		},
	}

	err := p.Run(makePC())
	if err == nil {
		t.Fatal("Run() expected error; got nil")
	}
	if secondCalled {
		t.Error("second phase was called after first phase failed; expected stop")
	}
}

func TestPipeline_PhaseErrorSynthesizesResult(t *testing.T) {
	pc := makePC()
	var buf strings.Builder
	pc.Writer = &buf

	p := &Pipeline{
		Phases: []Phase{
			{
				Name: "exploder",
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					return nil, errors.New("boom")
				},
			},
		},
	}
	p.Run(pc) //nolint:errcheck // intentional error case

	found := false
	for _, r := range pc.Results {
		if r.Name == "exploder" && r.Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected synthesized failed PhaseResult for 'exploder'; Results = %v", pc.Results)
	}
}

func TestPipeline_DryRunExitIsClean(t *testing.T) {
	var afterCalled bool

	p := &Pipeline{
		Phases: []Phase{
			{
				Name: "gate",
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					return &PhaseResult{Name: "gate", Status: "success"}, ErrDryRunExit
				},
			},
			stubPhase("after", &afterCalled, nil),
		},
	}

	err := p.Run(makePC())
	if err != nil {
		t.Fatalf("Run() returned error on ErrDryRunExit: %v", err)
	}
	if afterCalled {
		t.Error("phase after ErrDryRunExit should not have been called")
	}
}

func TestPipeline_HooksRunAfterPhases(t *testing.T) {
	var hookCalled bool

	p := &Pipeline{
		Phases: []Phase{stubPhase("phase", nil, nil)},
		Hooks: []PostBuildHook{
			{
				Name: "hook",
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					hookCalled = true
					return &PhaseResult{Name: "hook", Status: "success"}, nil
				},
			},
		},
	}

	if err := p.Run(makePC()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if !hookCalled {
		t.Error("hook was not called")
	}
}

func TestPipeline_HookSkippedWhenConditionFalse(t *testing.T) {
	var hookCalled bool

	p := &Pipeline{
		Phases: []Phase{stubPhase("phase", nil, nil)},
		Hooks: []PostBuildHook{
			{
				Name:      "cond-hook",
				Condition: func(pc *PipelineContext) bool { return false },
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					hookCalled = true
					return &PhaseResult{Name: "cond-hook", Status: "success"}, nil
				},
			},
		},
	}

	if err := p.Run(makePC()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if hookCalled {
		t.Error("hook with false Condition should not have been called")
	}
}

func TestPipeline_HookErrorIsNonFatal(t *testing.T) {
	p := &Pipeline{
		Phases: []Phase{stubPhase("phase", nil, nil)},
		Hooks: []PostBuildHook{
			{
				Name: "bad-hook",
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					return nil, errors.New("hook blew up")
				},
			},
		},
	}

	err := p.Run(makePC())
	if err != nil {
		t.Fatalf("hook error should be non-fatal; got: %v", err)
	}
}

func TestPipeline_HooksNotRunOnPhaseError(t *testing.T) {
	var hookCalled bool

	p := &Pipeline{
		Phases: []Phase{
			stubPhase("fail", nil, errors.New("phase error")),
		},
		Hooks: []PostBuildHook{
			{
				Name: "hook",
				Run: func(pc *PipelineContext) (*PhaseResult, error) {
					hookCalled = true
					return &PhaseResult{Name: "hook", Status: "success"}, nil
				},
			},
		},
	}

	p.Run(makePC()) //nolint:errcheck
	if hookCalled {
		t.Error("hooks should not run when a phase error stopped the pipeline")
	}
}

func TestPipeline_NilWriterDefaultsToStdout(t *testing.T) {
	// Just verify it doesn't panic with nil Writer — Pipeline.Run assigns os.Stdout.
	pc := makePC()
	pc.Writer = nil

	p := &Pipeline{Phases: []Phase{stubPhase("p", nil, nil)}}
	if err := p.Run(pc); err != nil {
		t.Fatalf("Run() with nil writer returned error: %v", err)
	}
	if pc.Writer == nil {
		t.Error("pc.Writer should be set to os.Stdout when initially nil")
	}
}

// --- LintPhase ---

func TestLintPhase_SkipWhenFlagSet(t *testing.T) {
	pc := makePC()
	pc.SkipLint = true

	phase := LintPhase()
	result, err := phase.Run(pc)
	if err != nil {
		t.Fatalf("LintPhase.Run() returned error: %v", err)
	}
	if result.Status != "skipped" {
		t.Errorf("LintPhase status = %q; want %q", result.Status, "skipped")
	}
}

// --- DryRunGate ---

func TestDryRunGate_ExitsWhenDryRun(t *testing.T) {
	pc := makePC()
	pc.DryRun = true

	var planCalled bool
	phase := DryRunGate(func(pc *PipelineContext) { planCalled = true })
	result, err := phase.Run(pc)

	if !errors.Is(err, ErrDryRunExit) {
		t.Fatalf("DryRunGate error = %v; want ErrDryRunExit", err)
	}
	if result == nil || result.Status != "success" {
		t.Errorf("DryRunGate result = %v; want success", result)
	}
	if !planCalled {
		t.Error("renderPlan was not called")
	}
}

func TestDryRunGate_SkipsWhenNotDryRun(t *testing.T) {
	pc := makePC()
	pc.DryRun = false

	phase := DryRunGate(nil)
	result, err := phase.Run(pc)

	if err != nil {
		t.Fatalf("DryRunGate returned error when DryRun=false: %v", err)
	}
	if result.Status != "skipped" {
		t.Errorf("DryRunGate status = %q; want %q", result.Status, "skipped")
	}
}

func TestDryRunGate_NilRenderPlanIsSafe(t *testing.T) {
	pc := makePC()
	pc.DryRun = true

	phase := DryRunGate(nil) // nil renderPlan must not panic
	_, err := phase.Run(pc)
	if !errors.Is(err, ErrDryRunExit) {
		t.Fatalf("DryRunGate error = %v; want ErrDryRunExit", err)
	}
}

// --- PublishManifestPhase ---

func TestPublishManifestPhase_SkipsWhenEmpty(t *testing.T) {
	pc := makePC()
	// Manifest is zero-value (no artifacts).

	phase := PublishManifestPhase()
	result, err := phase.Run(pc)
	if err != nil {
		t.Fatalf("PublishManifestPhase returned error: %v", err)
	}
	if result.Status != "skipped" {
		t.Errorf("status = %q; want %q", result.Status, "skipped")
	}
}

func TestPublishManifestPhase_WritesWhenHasArtifacts(t *testing.T) {
	tmpDir := t.TempDir()

	pc := makePC()
	pc.RootDir = tmpDir
	pc.Manifest = artifact.PublishManifest{
		Binaries: []artifact.PublishedBinary{
			{Name: "myapp", OS: "linux", Arch: "amd64", Path: "dist/myapp"},
		},
	}

	phase := PublishManifestPhase()
	result, err := phase.Run(pc)
	if err != nil {
		t.Fatalf("PublishManifestPhase returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q; want %q", result.Status, "success")
	}
}

// --- BannerPhase ---

func TestBannerPhase_RunsWithoutError(t *testing.T) {
	pc := makePC()
	pc.Writer = io.Discard

	phase := BannerPhase()
	result, err := phase.Run(pc)
	if err != nil {
		t.Fatalf("BannerPhase returned error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("status = %q; want %q", result.Status, "success")
	}
}

// --- CIContextKV ---

func TestCIContextKV_EmptyWhenNoEnvVars(t *testing.T) {
	// Unset all code-domain env vars — result should be empty.
	// CIContextKV emits only Commit + Branch/Tag. No platforms, no pipeline,
	// no runner. Those belong to DomainExecution and DomainPlan respectively.
	unsetEnvVars(t,
		"CI_PIPELINE_ID", "CI_RUNNER_DESCRIPTION",
		"CI_COMMIT_SHORT_SHA", "CI_COMMIT_SHA",
		"CI_COMMIT_BRANCH", "CI_COMMIT_TAG",
		"STAGEFREIGHT_PLATFORMS",
	)

	kv := CIContextKV()
	if len(kv) != 0 {
		t.Errorf("expected empty CIContextKV when no code env vars set; got %d entries", len(kv))
		for _, k := range kv {
			t.Logf("  unexpected: %q = %q (domain=%s)", k.Key, k.Value, k.Domain)
		}
	}
}

func TestCIContextKV_ReadsCIEnvVars(t *testing.T) {
	// Pipeline ID must NOT appear — it belongs to DomainExecution, not DomainCode.
	t.Setenv("CI_PIPELINE_ID", "99")
	t.Setenv("CI_COMMIT_SHORT_SHA", "abcd1234")
	t.Setenv("CI_COMMIT_BRANCH", "main")
	unsetEnvVars(t, "CI_RUNNER_DESCRIPTION", "CI_COMMIT_SHA", "CI_COMMIT_TAG", "STAGEFREIGHT_PLATFORMS")

	kv := CIContextKV()
	kvMap := make(map[string]string)
	for _, k := range kv {
		kvMap[k.Key] = k.Value
	}

	if kvMap["Commit"] != "abcd1234" {
		t.Errorf("Commit = %q; want %q", kvMap["Commit"], "abcd1234")
	}
	if kvMap["Branch"] != "main" {
		t.Errorf("Branch = %q; want %q", kvMap["Branch"], "main")
	}
	if _, hasPipeline := kvMap["Pipeline"]; hasPipeline {
		t.Error("Pipeline must not appear in CIContextKV — belongs to DomainExecution")
	}
	// All returned KVs must be DomainCode.
	for _, k := range kv {
		if k.Domain != output.DomainCode {
			t.Errorf("KV %q has domain %q; all CIContextKV entries must be DomainCode", k.Key, k.Domain)
		}
	}
}

func TestCIContextKV_PlatformsNotPresent(t *testing.T) {
	// Platforms belong to DomainPlan, never DomainCode.
	// STAGEFREIGHT_PLATFORMS must be ignored by CIContextKV.
	t.Setenv("STAGEFREIGHT_PLATFORMS", "linux/amd64,linux/arm64")
	unsetEnvVars(t, "CI_PIPELINE_ID", "CI_RUNNER_DESCRIPTION",
		"CI_COMMIT_SHORT_SHA", "CI_COMMIT_SHA",
		"CI_COMMIT_BRANCH", "CI_COMMIT_TAG")

	kv := CIContextKV()
	for _, k := range kv {
		if k.Key == "Platforms" {
			t.Errorf("Platforms must not appear in CIContextKV — belongs to DomainPlan")
		}
	}
}

func TestCIContextKV_TagWhenNoBranch(t *testing.T) {
	t.Setenv("CI_COMMIT_TAG", "v1.2.3")
	unsetEnvVars(t, "CI_COMMIT_BRANCH", "CI_PIPELINE_ID", "CI_RUNNER_DESCRIPTION",
		"CI_COMMIT_SHORT_SHA", "CI_COMMIT_SHA", "STAGEFREIGHT_PLATFORMS")

	kv := CIContextKV()
	kvMap := make(map[string]string)
	for _, k := range kv {
		kvMap[k.Key] = k.Value
	}

	if _, hasBranch := kvMap["Branch"]; hasBranch {
		t.Error("Branch should not be set when only CI_COMMIT_TAG is present")
	}
	if kvMap["Tag"] != "v1.2.3" {
		t.Errorf("Tag = %q; want %q", kvMap["Tag"], "v1.2.3")
	}
}

// --- CollectTargetsByKind ---

func TestCollectTargetsByKind_FiltersCorrectly(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.TargetConfig{
			{Kind: "docker", ID: "a"},
			{Kind: "binary", ID: "b"},
			{Kind: "docker", ID: "c"},
		},
	}

	got := CollectTargetsByKind(cfg, "docker")
	if len(got) != 2 {
		t.Fatalf("CollectTargetsByKind(docker) = %d targets; want 2", len(got))
	}
	for _, t2 := range got {
		if t2.Kind != "docker" {
			t.Errorf("got target with kind %q; want docker", t2.Kind)
		}
	}
}

func TestCollectTargetsByKind_EmptyWhenNoneMatch(t *testing.T) {
	cfg := &config.Config{
		Targets: []config.TargetConfig{
			{Kind: "docker", ID: "a"},
		},
	}
	got := CollectTargetsByKind(cfg, "binary")
	if len(got) != 0 {
		t.Errorf("CollectTargetsByKind(binary) = %d; want 0", len(got))
	}
}

// --- renderSummary ---

func TestRenderSummary_SkipsBanner(t *testing.T) {
	var buf strings.Builder
	pc := makePC()
	pc.Writer = &buf
	pc.Results = []PhaseResult{
		{Name: "banner", Status: "success"},
		{Name: "lint", Status: "success", Summary: "clean"},
	}

	renderSummary(pc)

	out := buf.String()
	if strings.Contains(out, "banner") {
		t.Error("renderSummary should skip the 'banner' phase from summary output")
	}
}

func TestRenderSummary_SkipsInactiveDryRun(t *testing.T) {
	var buf strings.Builder
	pc := makePC()
	pc.Writer = &buf
	pc.Results = []PhaseResult{
		{Name: "dry-run", Status: "skipped"},
		{Name: "lint", Status: "success", Summary: "clean"},
	}

	renderSummary(pc)

	out := buf.String()
	if strings.Contains(out, "dry-run") {
		t.Error("renderSummary should skip an inactive 'dry-run' phase from summary output")
	}
}

func TestRenderSummary_EmptyResultsIsNoop(t *testing.T) {
	var buf strings.Builder
	pc := makePC()
	pc.Writer = &buf
	// pc.Results is nil/empty

	renderSummary(pc)

	if buf.Len() != 0 {
		t.Errorf("renderSummary with empty results wrote %d bytes; want 0", buf.Len())
	}
}

func TestRenderSummary_FailedPhaseShowsOverallFailed(t *testing.T) {
	var buf strings.Builder
	pc := makePC()
	pc.Writer = &buf
	pc.Results = []PhaseResult{
		{Name: "lint", Status: "failed", Summary: "3 critical"},
	}

	renderSummary(pc)

	out := buf.String()
	// renderSummary uses ✗ for failed rows rather than the literal text "failed".
	if !strings.Contains(out, "✗") {
		t.Errorf("expected failure indicator (✗) in summary output; got:\n%s", out)
	}
}

// --- ErrDryRunExit sentinel ---

func TestErrDryRunExit_IsDistinct(t *testing.T) {
	if ErrDryRunExit == nil {
		t.Fatal("ErrDryRunExit must not be nil")
	}
	if errors.Is(ErrDryRunExit, fmt.Errorf("other error")) {
		t.Error("ErrDryRunExit should not match arbitrary errors")
	}
}

// --- helpers ---

// unsetEnvVars clears env vars for the duration of the test.
func unsetEnvVars(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
	}
}
