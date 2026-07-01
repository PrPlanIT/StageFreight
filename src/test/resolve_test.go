package test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func TestGoArgv_FullFlagProjection(t *testing.T) {
	tr := true
	s := config.TestSuite{
		Tool:     config.TestToolGo,
		Tags:     []string{"integration"},
		Run:      "TestFoo",
		Timeout:  "10m",
		Race:     &tr,
		Packages: []string{"./src/..."},
		Args:     []string{"-v"},
	}
	got := goArgv(s)
	want := []string{"go", "test", "-tags", "integration", "-run", "TestFoo", "-timeout", "10m", "-race", "./src/...", "-v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("goArgv = %v\n want %v", got, want)
	}
}

func TestGoArgv_DefaultsToDotDotDot(t *testing.T) {
	got := goArgv(config.TestSuite{Tool: config.TestToolGo})
	want := []string{"go", "test", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("goArgv = %v, want %v", got, want)
	}
}

func TestRustArgv_WorkspaceFeaturesTestsRelease(t *testing.T) {
	tr := true
	s := config.TestSuite{Tool: config.TestToolRust, Features: []string{"integration"}, Tests: []string{"api"}, Release: &tr}
	got := rustArgv(s, true) // --workspace inferred from manifest
	want := []string{"cargo", "test", "--no-fail-fast", "--workspace", "--release", "--features", "integration", "--test", "api"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rustArgv = %v\n want %v", got, want)
	}
}

func TestRustArgv_Nextest(t *testing.T) {
	tr := true
	got := rustArgv(config.TestSuite{Tool: config.TestToolRust, Nextest: &tr}, false)
	want := []string{"cargo", "nextest", "run", "--no-fail-fast"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rustArgv = %v, want %v", got, want)
	}
}

func TestEffectiveGate_DefaultsToPerform(t *testing.T) {
	if g := (config.TestSuite{}).EffectiveGate(); g != config.GatePerform {
		t.Errorf("default gate = %q, want perform", g)
	}
	if g := (config.TestSuite{Gate: config.GateAdvisory}).EffectiveGate(); g != config.GateAdvisory {
		t.Errorf("gate = %q, want advisory", g)
	}
}

func TestVerdict_AdvisoryDoesNotGate(t *testing.T) {
	r := TestResult{Suites: []SuiteResult{
		{ID: "flaky", Gate: config.GateAdvisory, Status: StatusFailed},
	}}
	if r.FailedNonAdvisory() {
		t.Error("an advisory failure must NOT gate")
	}
	if !r.Failed() {
		t.Error("Failed() should report the advisory failure")
	}
	r.Suites = append(r.Suites, SuiteResult{ID: "unit", Gate: config.GatePerform, Status: StatusFailed})
	if !r.FailedNonAdvisory() {
		t.Error("a perform-gated failure must gate")
	}
}

func TestResolve_ScriptSuite(t *testing.T) {
	cfg := &config.Config{Test: config.TestConfig{Enabled: true, Suites: []config.TestSuite{
		{ID: "e2e", Tool: config.TestToolScript, Command: "echo hi"},
	}}}
	suites, err := Resolve(cfg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) != 1 || len(suites[0].Argv) != 3 || suites[0].Argv[0] != "sh" || suites[0].Argv[1] != "-c" {
		t.Errorf("script suite resolution: %+v", suites)
	}
}

func TestResolve_DisabledOrAutoOff(t *testing.T) {
	if s, _ := Resolve(&config.Config{Test: config.TestConfig{Enabled: false}}, t.TempDir()); len(s) != 0 {
		t.Error("disabled test config must resolve to no suites")
	}
	no := false
	if s, _ := Resolve(&config.Config{Test: config.TestConfig{Enabled: true, Auto: &no}}, t.TempDir()); len(s) != 0 {
		t.Error("auto:false with no suites must resolve to no suites")
	}
}

func TestResolve_SynthesizeGoDefaultFromBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Test:   config.TestConfig{Enabled: true},
		Builds: []config.BuildConfig{{ID: "bin", Kind: "binary", Builder: "go", From: "./cmd"}},
	}
	suites, err := Resolve(cfg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) != 1 {
		t.Fatalf("want 1 synthesized suite, got %d", len(suites))
	}
	s := suites[0]
	if !s.Synthesized || s.ID != "go-default-bin" || s.Gate != config.GatePerform {
		t.Errorf("synthesized suite wrong: %+v", s)
	}
	if want := []string{"go", "test", "./..."}; !reflect.DeepEqual(s.Argv, want) {
		t.Errorf("argv = %v, want %v", s.Argv, want)
	}
	if s.Dir != dir {
		t.Errorf("dir = %q, want module root %q", s.Dir, dir)
	}
}

func TestResolve_DuplicateIDRejected(t *testing.T) {
	cfg := &config.Config{Test: config.TestConfig{Enabled: true, Suites: []config.TestSuite{
		{ID: "x", Tool: config.TestToolScript, Command: "true"},
		{ID: "x", Tool: config.TestToolScript, Command: "true"},
	}}}
	if _, err := Resolve(cfg, t.TempDir()); err == nil {
		t.Error("duplicate suite id must be rejected")
	}
}
