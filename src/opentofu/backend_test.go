package opentofu

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// fakeBinary records each invocation and answers by subcommand.
type fakeBinary struct {
	calls    [][]string
	envs     [][]string
	planExit int
	showJSON string
	failStep string
}

func (f *fakeBinary) run(_ context.Context, _, _ string, env, args []string, out io.Writer) (int, error) {
	f.calls = append(f.calls, args)
	f.envs = append(f.envs, env)
	if args[0] == f.failStep {
		io.WriteString(out, "boom\n")
		return 1, nil
	}
	switch args[0] {
	case "plan":
		return f.planExit, nil
	case "show":
		io.WriteString(out, f.showJSON)
	}
	return 0, nil
}

func (f *fakeBinary) verbs() []string {
	var v []string
	for _, c := range f.calls {
		v = append(v, c[0])
	}
	return v
}

func newTestBackend(t *testing.T, fb *fakeBinary, roots ...config.OpenTofuRoot) (*Backend, *config.Config, *runtime.RuntimeContext) {
	t.Helper()
	repo := t.TempDir()
	for _, r := range roots {
		if err := os.MkdirAll(filepath.Join(repo, r.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, vf := range r.VarFiles {
			if err := os.WriteFile(filepath.Join(repo, r.Path, vf), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := &config.Config{OpenTofu: config.OpenTofuConfig{Backend: "opentofu", Credentials: "OCI", Roots: roots}}
	b := &Backend{
		tool:    "opentofu",
		runner:  fb.run,
		environ: func() []string { return []string{"OCI_TF_VAR_region=us-sanjose-1", "HOME=/h"} },
		resolver: func(context.Context, string, string, map[string]config.ToolConstraint) (toolchain.Result, error) {
			return toolchain.Result{Path: "/bin/tofu", Version: "1.13.9"}, nil
		},
	}
	return b, cfg, &runtime.RuntimeContext{RepoRoot: repo}
}

const replacePlan = `{"resource_changes":[
 {"change":{"actions":["create"]}},
 {"change":{"actions":["update"]}},
 {"change":{"actions":["delete","create"]}},
 {"change":{"actions":["no-op"]}},
 {"change":{"actions":["read"]}}]}`

func TestParsePlanChanges(t *testing.T) {
	c, err := ParsePlanChanges([]byte(replacePlan))
	if err != nil {
		t.Fatal(err)
	}
	if c != (Changes{Add: 2, Change: 1, Destroy: 1}) {
		t.Errorf("changes = %+v; a replacement counts as one add and one destroy", c)
	}
	if _, err := ParsePlanChanges([]byte("not json")); err == nil {
		t.Error("unreadable plan must error")
	}
}

// TestConverge_AppliesTheSavedPlan: init → workspace → plan -out → show → apply <same file>,
// with the credentials stripped of their prefix and the automation switches set.
func TestConverge_AppliesTheSavedPlan(t *testing.T) {
	fb := &fakeBinary{planExit: 2, showJSON: replacePlan}
	root := config.OpenTofuRoot{ID: "site", Path: "opentofu/site", Converge: true, Workspace: "production", VarFiles: []string{"production.tfvars"}}
	b, cfg, rctx := newTestBackend(t, fb, root)

	if err := runtime.RunLifecycleWith(context.Background(), cfg, rctx, Mode, b); err != nil {
		t.Fatal(err)
	}
	if got := fb.verbs(); !slices.Equal(got, []string{"init", "workspace", "plan", "show", "apply"}) {
		t.Fatalf("steps = %v", got)
	}
	if !slices.Contains(fb.calls[0], "-lockfile=readonly") {
		t.Errorf("init must not rewrite the lock file: %v", fb.calls[0])
	}
	if !slices.Equal(fb.calls[1], []string{"workspace", "select", "-or-create", "production"}) {
		t.Errorf("workspace call = %v", fb.calls[1])
	}
	plan := fb.calls[2]
	if !slices.Contains(plan, "-detailed-exitcode") || !slices.Contains(plan, "-var-file=production.tfvars") {
		t.Errorf("plan call = %v", plan)
	}
	var planFile string
	for _, a := range plan {
		if strings.HasPrefix(a, "-out=") {
			planFile = strings.TrimPrefix(a, "-out=")
		}
	}
	if apply := fb.calls[4]; apply[len(apply)-1] != planFile || planFile == "" {
		t.Errorf("apply %v must apply the saved plan %q", apply, planFile)
	}
	env := strings.Join(fb.envs[2], "\n")
	for _, want := range []string{"TF_VAR_region=us-sanjose-1", "TF_IN_AUTOMATION=1", "TF_INPUT=0", "TF_DATA_DIR="} {
		if !strings.Contains(env, want) {
			t.Errorf("plan env lacks %q", want)
		}
	}

	res := b.RootResults()
	if len(res) != 1 || !res[0].Applied || res[0].Err != nil || res[0].Changes != (Changes{2, 1, 1}) {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(planFile); !os.IsNotExist(err) {
		t.Errorf("plan directory must be cleaned up, %s: %v", planFile, err)
	}
}

func TestConverge_NoChangesSkipsApply(t *testing.T) {
	fb := &fakeBinary{planExit: 0}
	b, cfg, rctx := newTestBackend(t, fb, config.OpenTofuRoot{ID: "site", Path: "site", Converge: true})
	if err := runtime.RunLifecycleWith(context.Background(), cfg, rctx, Mode, b); err != nil {
		t.Fatal(err)
	}
	if got := fb.verbs(); !slices.Equal(got, []string{"init", "plan"}) {
		t.Errorf("steps = %v; no workspace declared, and nothing to apply", got)
	}
	if r := b.RootResults()[0]; r.Applied || r.Summary() != "no changes" {
		t.Errorf("result = %+v", r)
	}
}

func TestDryRun_PlansWithoutApplying(t *testing.T) {
	fb := &fakeBinary{planExit: 2, showJSON: replacePlan}
	b, cfg, rctx := newTestBackend(t, fb, config.OpenTofuRoot{ID: "site", Path: "site", Converge: true})
	rctx.DryRun = true
	if err := runtime.RunLifecycleWith(context.Background(), cfg, rctx, Mode, b); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fb.verbs(), "apply") {
		t.Errorf("dry-run applied: %v", fb.verbs())
	}
	if got := b.RootResults()[0].Summary(); got != "plan: 2 to add, 1 to change, 1 to destroy" {
		t.Errorf("summary = %q", got)
	}
}

func TestConverge_FailsLoud(t *testing.T) {
	for _, step := range []string{"init", "plan", "apply"} {
		fb := &fakeBinary{planExit: 2, showJSON: replacePlan, failStep: step}
		b, cfg, rctx := newTestBackend(t, fb, config.OpenTofuRoot{ID: "site", Path: "site", Converge: true})
		if err := runtime.RunLifecycleWith(context.Background(), cfg, rctx, Mode, b); err != nil {
			t.Fatal(err)
		}
		r := b.RootResults()[0]
		if r.Err == nil || r.Applied || !strings.Contains(r.Output, "boom") {
			t.Errorf("%s failure: result = %+v", step, r)
		}
		if res := rctx.Result(); res == nil || res.Actions[0].Success {
			t.Errorf("%s failure must fail the action", step)
		}
	}
}

func TestPrepare_RefusesWithoutCredentials(t *testing.T) {
	fb := &fakeBinary{}
	b, cfg, rctx := newTestBackend(t, fb, config.OpenTofuRoot{ID: "site", Path: "site", Converge: true})
	b.environ = func() []string { return []string{"HOME=/h"} }
	err := runtime.RunLifecycleWith(context.Background(), cfg, rctx, Mode, b)
	if err == nil || !strings.Contains(err.Error(), "OCI_*") {
		t.Errorf("err = %v", err)
	}
	if len(fb.calls) != 0 {
		t.Errorf("binary ran without credentials: %v", fb.verbs())
	}
}

func TestValidate_MissingVarFile(t *testing.T) {
	fb := &fakeBinary{}
	b, cfg, rctx := newTestBackend(t, fb, config.OpenTofuRoot{ID: "site", Path: "site", Converge: true})
	cfg.OpenTofu.Roots[0].VarFiles = []string{"absent.tfvars"}
	err := runtime.RunLifecycleWith(context.Background(), cfg, rctx, Mode, b)
	if err == nil || !strings.Contains(err.Error(), "opentofu.roots.site.var_files absent.tfvars") {
		t.Errorf("err = %v", err)
	}
}

func TestRegistered(t *testing.T) {
	for _, name := range []string{"opentofu", "terraform"} {
		lb, err := runtime.ResolveBackend(Mode, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lb.Name() != name {
			t.Errorf("backend %s reports name %q", name, lb.Name())
		}
	}
}
