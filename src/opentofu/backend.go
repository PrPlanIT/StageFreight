// Package opentofu implements the infrastructure-convergence lifecycle backend:
// declared root modules planned and applied by OpenTofu or Terraform, the binary
// resolved through the toolchain. The package returns DATA — plans and per-root
// results; all terminal rendering belongs to cli/cmd.
package opentofu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/provision"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// Mode is the lifecycle mode both backends register under.
const Mode = "opentofu"

func init() {
	for _, name := range []string{config.OpenTofuBackendOpenTofu, config.OpenTofuBackendTerraform} {
		tool := name
		runtime.Register(Mode, tool, func() runtime.LifecycleBackend {
			return &Backend{tool: tool}
		})
	}
}

// Backend implements runtime.LifecycleBackend for one binary (opentofu or terraform).
//
// Scope defaults to the config's converge set (perform reconcile). SelectRoot narrows
// it to one named entry — the `opentofu run <id>` path, which is also the ONLY way a
// converge:false runbook ever executes.
type Backend struct {
	tool     string // toolchain name: opentofu | terraform
	bin      string // resolved binary path
	version  string // resolved binary version
	roots    []config.OpenTofuRoot
	credEnv  []string // NAME=value credentials, prefix stripped
	planDir  string   // per-run directory holding saved plan files
	results  []RootResult
	stream   func(rootID string) io.WriteCloser // live-output sink factory (cmd-owned)
	runner   func(ctx context.Context, bin, dir string, env, args []string, out io.Writer) (int, error)
	environ  func() []string
	resolver func(ctx context.Context, repoRoot, tool string, want map[string]config.ToolConstraint) (toolchain.Result, error)
}

// SetStream supplies a per-root live-output sink factory. Nil disables streaming.
func (b *Backend) SetStream(f func(rootID string) io.WriteCloser) { b.stream = f }

// SelectRoot narrows execution to a single declared root (the run verb).
func (b *Backend) SelectRoot(r config.OpenTofuRoot) { b.roots = []config.OpenTofuRoot{r} }

// RootResults returns the per-root outcomes after Execute (or the plans after a dry-run Plan).
func (b *Backend) RootResults() []RootResult { return b.results }

// Identity returns the resolved binary and version, for the results box.
func (b *Backend) Identity() string {
	if b.bin == "" {
		return ""
	}
	return fmt.Sprintf("%s %s", b.tool, b.version)
}

func (b *Backend) Name() string { return b.tool }

func (b *Backend) Capabilities() []runtime.Capability {
	return []runtime.Capability{
		runtime.CapReconcile,
		runtime.CapDryRun,
		runtime.CapPlanExecute,
	}
}

// Validate resolves the binary through the toolchain and checks the declared files.
func (b *Backend) Validate(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	o := cfg.OpenTofu
	if len(b.roots) == 0 {
		b.roots = o.ConvergeRoots()
	}
	if len(b.roots) == 0 {
		return fmt.Errorf("no opentofu roots in scope")
	}

	resolve := b.resolver
	if resolve == nil {
		resolve = resolveTool
	}
	res, err := resolve(ctx, rctx.RepoRoot, b.tool, cfg.Toolchains.Want)
	if err != nil {
		return fmt.Errorf("%s: %w", b.tool, err)
	}
	b.bin, b.version = res.Path, res.Version

	for _, r := range b.roots {
		dir := filepath.Join(rctx.RepoRoot, r.Path)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("opentofu.roots.%s.path %s: not a directory", r.ID, r.Path)
		}
		for _, vf := range r.VarFiles {
			if _, err := os.Stat(filepath.Join(dir, vf)); err != nil {
				return fmt.Errorf("opentofu.roots.%s.var_files %s: %w", r.ID, vf, err)
			}
		}
	}
	return nil
}

// resolveTool provisions the binary from toolchains.want.<tool>.
func resolveTool(ctx context.Context, repoRoot, tool string, want map[string]config.ToolConstraint) (toolchain.Result, error) {
	sel, selErr := toolchain.Select(repoRoot, tool, want)
	res, err := provision.ResolveSelection(ctx, repoRoot, tool, sel, selErr, "OpenTofu converge")
	if err != nil && (sel.Pinned || selErr != nil) {
		return res, fmt.Errorf("pinned version %s failed to resolve: %w", sel.Version, err)
	}
	return res, err
}

// Prepare reads the credentials and creates the per-run directory that holds saved plans
// and each root's data directory, keeping .terraform/ out of the checkout.
func (b *Backend) Prepare(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	environ := b.environ
	if environ == nil {
		environ = os.Environ
	}
	b.credEnv = cfg.OpenTofu.CredentialEnv(environ())
	if len(b.credEnv) == 0 {
		return fmt.Errorf("no %s_* credentials set — opentofu credentials unavailable in this context", cfg.OpenTofu.EnvPrefix())
	}
	dir, err := os.MkdirTemp("", "sf-opentofu-*")
	if err != nil {
		return err
	}
	b.planDir = dir
	rctx.Resolved.AddCleanup(func() { os.RemoveAll(dir) })
	return nil
}

// Plan describes the roots in scope. In dry-run it plans each root against live state —
// the plan IS the dry-run. In execute mode it stays off the network: Execute plans and
// applies that same saved plan, so planning here too would double every converge.
func (b *Backend) Plan(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) (*runtime.LifecyclePlan, error) {
	plan := &runtime.LifecyclePlan{Mode: Mode, Backend: b.tool, DryRun: rctx.DryRun}
	for i, r := range b.roots {
		action := runtime.PlannedAction{
			Name:        r.ID,
			Description: fmt.Sprintf("converge %s (workspace %s)", r.Path, workspaceName(r)),
			Order:       i + 1,
			Action:      "converge",
			Metadata:    map[string]string{"path": r.Path, "workspace": workspaceName(r)},
		}
		if rctx.DryRun {
			res := b.runRoot(ctx, rctx, r, false)
			b.results = append(b.results, res)
			action.Description += " — " + res.Summary()
		}
		plan.Actions = append(plan.Actions, action)
	}
	return plan, nil
}

// Execute plans and applies each root in scope, in declared order, applying exactly the
// plan it just made. FAIL LOUD: a failed init, plan or apply fails that root's action.
func (b *Backend) Execute(ctx context.Context, plan *runtime.LifecyclePlan, rctx *runtime.RuntimeContext) (*runtime.LifecycleResult, error) {
	b.results = nil
	var results []runtime.ActionResult
	for _, r := range b.roots {
		start := time.Now()
		res := b.runRoot(ctx, rctx, r, true)
		b.results = append(b.results, res)

		ar := runtime.ActionResult{Name: r.ID, Duration: time.Since(start), Success: res.Err == nil, Message: res.Summary()}
		if res.Err != nil {
			ar.Stderr = tail(res.Output, 2000)
		}
		results = append(results, ar)
	}
	return &runtime.LifecycleResult{Actions: results}, nil
}

func (b *Backend) Cleanup(rctx *runtime.RuntimeContext) {
	// Plan directory removal is registered via rctx.Resolved.AddCleanup in Prepare.
}

// RootResult is the outcome of one root: its plan and, when applied, the apply.
type RootResult struct {
	ID      string
	Path    string
	Changes Changes
	Applied bool   // apply ran and succeeded
	Output  string // combined binary output
	Err     error  // the failing step, nil on success
}

// Summary is the one-line outcome.
func (r RootResult) Summary() string {
	switch {
	case r.Err != nil:
		return r.Err.Error()
	case r.Changes.None():
		return "no changes"
	case r.Applied:
		return "applied: " + r.Changes.String()
	default:
		return "plan: " + r.Changes.String()
	}
}

// runRoot runs one root: init, workspace, plan to a saved file, read the plan's changes,
// and — when apply is set and the plan changes anything — apply that saved plan.
func (b *Backend) runRoot(ctx context.Context, rctx *runtime.RuntimeContext, r config.OpenTofuRoot, apply bool) (res RootResult) {
	res = RootResult{ID: r.ID, Path: r.Path}

	var buf bytes.Buffer
	dest := io.Writer(&buf)
	var sink io.WriteCloser
	if b.stream != nil {
		sink = b.stream(r.ID)
		dest = io.MultiWriter(&buf, sink)
	}
	defer func() {
		if sink != nil {
			sink.Close()
		}
		res.Output = buf.String()
	}()

	dir := filepath.Join(rctx.RepoRoot, r.Path)
	env := append(b.env(), "TF_DATA_DIR="+filepath.Join(b.planDir, r.ID+".data"))
	run := func(step string, args ...string) (int, error) {
		code, err := b.exec(ctx, dir, env, args, dest)
		if err != nil {
			return code, fmt.Errorf("%s: %w", step, err)
		}
		return code, nil
	}
	fail := func(step string, code int, err error) RootResult {
		if err == nil {
			err = fmt.Errorf("%s exited %d", step, code)
		}
		res.Err = err
		return res
	}

	if code, err := run("init", "init", "-input=false", "-lockfile=readonly"); err != nil || code != 0 {
		return fail("init", code, err)
	}
	if r.Workspace != "" {
		if code, err := run("workspace", "workspace", "select", "-or-create", r.Workspace); err != nil || code != 0 {
			return fail("workspace select", code, err)
		}
	}

	planFile := filepath.Join(b.planDir, r.ID+".tfplan")
	planArgs := []string{"plan", "-input=false", "-detailed-exitcode", "-out=" + planFile}
	for _, vf := range r.VarFiles {
		planArgs = append(planArgs, "-var-file="+vf)
	}
	code, err := run("plan", planArgs...)
	if err != nil {
		return fail("plan", code, err)
	}
	switch code {
	case 0:
		return res // no changes
	case 2:
	default:
		return fail("plan", code, nil)
	}

	var js bytes.Buffer
	if code, err := b.exec(ctx, dir, env, []string{"show", "-json", planFile}, &js); err != nil || code != 0 {
		return fail("show", code, err)
	}
	changes, err := ParsePlanChanges(js.Bytes())
	if err != nil {
		return fail("show", 0, fmt.Errorf("reading plan: %w", err))
	}
	res.Changes = changes

	if !apply || changes.None() {
		return res
	}
	if code, err := run("apply", "apply", "-input=false", planFile); err != nil || code != 0 {
		return fail("apply", code, err)
	}
	res.Applied = true
	return res
}

// env is the binary's environment: the job's, the credentials with their prefix
// stripped (later entries win), and the automation switches.
func (b *Backend) env() []string {
	environ := b.environ
	if environ == nil {
		environ = os.Environ
	}
	env := append([]string{}, environ()...)
	env = append(env, b.credEnv...)
	return append(env, "TF_IN_AUTOMATION=1", "TF_INPUT=0")
}

func (b *Backend) exec(ctx context.Context, dir string, env, args []string, out io.Writer) (int, error) {
	run := b.runner
	if run == nil {
		run = execRun
	}
	return run(ctx, b.bin, dir, env, args, out)
}

// execRun runs the binary; a nonzero exit is a code, not an error.
func execRun(ctx context.Context, bin, dir string, env, args []string, out io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func workspaceName(r config.OpenTofuRoot) string {
	if r.Workspace == "" {
		return "default"
	}
	return r.Workspace
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
