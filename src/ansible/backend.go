// Package ansible implements the host-convergence lifecycle backend: declared
// playbooks executed inside a containerized ansible runtime (the execution
// image) against SSH-managed hosts. The package returns DATA — plans, recaps,
// aggregates; all terminal rendering belongs to cli/cmd.
package ansible

import (
	"context"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/docker"
	"github.com/PrPlanIT/StageFreight/src/runtime"
	gossh "golang.org/x/crypto/ssh"
)

func init() {
	runtime.Register("ansible", "ansible", func() runtime.LifecycleBackend {
		return &Backend{}
	})
}

// containerSSHDir is where the key material lands inside the execution image.
const (
	containerWorkdir    = "/work"
	containerKeyPath    = "/stagefreight/ssh/key"
	containerKnownHosts = "/stagefreight/ssh/known_hosts"
)

// Backend implements runtime.LifecycleBackend for containerized ansible.
//
// Scope defaults to the config's converge set (perform reconcile). SelectPlay
// narrows it to one named entry — the `ansible run <id>` path, which is also
// the ONLY way a converge:false runbook ever executes.
type Backend struct {
	cfg       *config.Config // captured at Validate; Execute composes container args from it
	dockerBin string
	imageRef  string // config reference
	imageID   string // resolved digest/ID recorded at Validate
	keyPath   string // 0600 tmpfile holding the (decrypted) private key
	plays     []config.AnsiblePlaybook
	extraVars []string // -e key=val passthrough (run verb)
	results   []PlayResult
}

// SelectPlay narrows execution to a single declared play (the run verb).
func (b *Backend) SelectPlay(p config.AnsiblePlaybook) { b.plays = []config.AnsiblePlaybook{p} }

// SetExtraVars supplies launch-time -e key=val pairs (the run verb).
func (b *Backend) SetExtraVars(vars []string) { b.extraVars = vars }

// PlayResults returns the structured per-play outcomes after Execute (or the
// check results after a dry-run Plan).
func (b *Backend) PlayResults() []PlayResult { return b.results }

// Aggregate returns the unique-host rollup feeding the ansible.* facts.
func (b *Backend) Aggregate() Aggregate { return AggregateResults(b.results) }

// ImageRef returns the resolved execution image reference (for trust display).
func (b *Backend) ImageRef() string { return b.imageRef }

// ImageID returns the image identity recorded at Validate.
func (b *Backend) ImageID() string { return b.imageID }

// CredentialsPresent reports whether the SSH key material this config needs is
// available in the environment. The key ships as a forge-protected variable,
// so MR/unprotected-ref pipelines legitimately lack it — dispatch uses this
// for a clear "skipped" instead of a mid-run auth failure.
func CredentialsPresent(cfg *config.Config) bool {
	return os.Getenv(cfg.Ansible.SSH.EnvPrefix()+"_SSH_KEY") != ""
}

func (b *Backend) Name() string { return "ansible" }

func (b *Backend) Capabilities() []runtime.Capability {
	return []runtime.Capability{
		runtime.CapReconcile,
		runtime.CapDryRun,
		runtime.CapPlanExecute,
	}
}

// Validate resolves the execution substrate: docker, the pinned execution
// image (pulled if absent, identity recorded), and the declared files.
func (b *Backend) Validate(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	b.cfg = cfg
	a := cfg.Ansible
	if len(b.plays) == 0 {
		b.plays = a.ConvergePlaybooks()
	}
	if len(b.plays) == 0 {
		return fmt.Errorf("no ansible playbooks in scope")
	}

	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker unavailable: ansible runs in the execution image (%s)", a.Image)
	}
	b.dockerBin = dockerBin
	b.imageRef = a.Image

	// Resolve the image: local inspect first, pull on miss, then record the
	// identity actually used — the digest is the trust statement.
	if _, err := b.docker(ctx, "image", "inspect", "--format", "{{.Id}}", a.Image); err != nil {
		if out, perr := b.docker(ctx, "pull", a.Image); perr != nil {
			return fmt.Errorf("execution image %s unavailable: %s", a.Image, firstLine(out))
		}
	}
	id, err := b.docker(ctx, "image", "inspect", "--format", "{{.Id}}", a.Image)
	if err != nil {
		return fmt.Errorf("execution image %s: inspect failed after pull", a.Image)
	}
	b.imageID = strings.TrimSpace(id)

	if _, err := os.Stat(filepath.Join(rctx.RepoRoot, a.Inventory)); err != nil {
		return fmt.Errorf("ansible.inventory %s: %w", a.Inventory, err)
	}
	for _, p := range b.plays {
		if _, err := os.Stat(filepath.Join(rctx.RepoRoot, p.Path)); err != nil {
			return fmt.Errorf("ansible.playbooks.%s.path %s: %w", p.ID, p.Path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(rctx.RepoRoot, a.SSH.KnownHosts)); err != nil {
		return fmt.Errorf("ansible.ssh.known_hosts %s: %w (host-key verification is strict — commit the fleet's host keys)", a.SSH.KnownHosts, err)
	}
	return nil
}

// Prepare materializes the SSH identity: the key from <PREFIX>_SSH_KEY
// (decrypted via <PREFIX>_SSH_KEY_PASSPHRASE when set) into a 0600 tmpfile
// with registered cleanup, and validates every play's groups against the
// inventory so an empty --limit fails here, not silently inside ansible.
func (b *Backend) Prepare(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	a := cfg.Ansible
	prefix := a.SSH.EnvPrefix()
	keyPEM := os.Getenv(prefix + "_SSH_KEY")
	if keyPEM == "" {
		return fmt.Errorf("%s_SSH_KEY not set — ansible credentials unavailable in this context", prefix)
	}

	keyBytes := []byte(keyPEM)
	if pass := os.Getenv(prefix + "_SSH_KEY_PASSPHRASE"); pass != "" {
		raw, err := gossh.ParseRawPrivateKeyWithPassphrase(keyBytes, []byte(pass))
		if err != nil {
			return fmt.Errorf("%s_SSH_KEY: decrypting with passphrase: %w", prefix, err)
		}
		block, err := gossh.MarshalPrivateKey(raw, "")
		if err != nil {
			return fmt.Errorf("%s_SSH_KEY: re-encoding decrypted key: %w", prefix, err)
		}
		keyBytes = pem.EncodeToMemory(block)
	} else if _, err := gossh.ParsePrivateKey(keyBytes); err != nil {
		return fmt.Errorf("%s_SSH_KEY: %w", prefix, err)
	}

	tmp, err := os.CreateTemp("", "sf-ansible-key-*")
	if err != nil {
		return fmt.Errorf("materializing ssh key: %w", err)
	}
	keyPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(keyPath)
		return fmt.Errorf("materializing ssh key: %w", err)
	}
	if _, err := tmp.Write(keyBytes); err != nil {
		tmp.Close()
		os.Remove(keyPath)
		return fmt.Errorf("materializing ssh key: %w", err)
	}
	tmp.Close()
	b.keyPath = keyPath
	rctx.Resolved.AddCleanup(func() { os.Remove(keyPath) })

	inv := &docker.AnsibleInventory{Path: filepath.Join(rctx.RepoRoot, a.Inventory)}
	for _, p := range b.plays {
		hosts, err := inv.Resolve(ctx, docker.TargetSelector{Groups: p.Groups})
		if err != nil {
			return fmt.Errorf("ansible.playbooks.%s: %w", p.ID, err)
		}
		if len(hosts) == 0 {
			return fmt.Errorf("ansible.playbooks.%s: groups %v resolve to zero hosts in %s", p.ID, p.Groups, a.Inventory)
		}
	}
	return nil
}

// Plan describes the plays in scope. In dry-run it runs each play with
// --check --diff — a real (approximate) plan against the live hosts; the
// terraform plan/apply idiom. In execute mode it stays off the network: the
// apply itself reports per-host truth, and doubling every converge with a
// full --check SSH pass would tax the fleet for information the recap
// delivers minutes later anyway.
func (b *Backend) Plan(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) (*runtime.LifecyclePlan, error) {
	plan := &runtime.LifecyclePlan{Mode: "ansible", Backend: "ansible", DryRun: rctx.DryRun}

	for i, p := range b.plays {
		action := runtime.PlannedAction{
			Name:        p.ID,
			Description: fmt.Sprintf("converge %s (groups %s)", p.Path, strings.Join(p.Groups, ", ")),
			Order:       i + 1,
			Action:      "converge",
			Metadata:    map[string]string{"path": p.Path, "groups": strings.Join(p.Groups, ",")},
		}
		if rctx.DryRun {
			res := b.runPlay(ctx, cfg, rctx, p, true)
			b.results = append(b.results, res)
			agg := AggregateResults([]PlayResult{res})
			action.Metadata["check_changed"] = fmt.Sprintf("%d", agg.Changed)
			action.Metadata["check_hosts"] = fmt.Sprintf("%d", agg.Total)
			if failed := res.FailedHosts(); len(failed) > 0 {
				action.Description += fmt.Sprintf(" — check reports issues on: %s", strings.Join(failed, ", "))
			} else {
				action.Description += fmt.Sprintf(" — check: %d/%d hosts, %d with pending changes", agg.Total, agg.Total, agg.Changed)
			}
		}
		plan.Actions = append(plan.Actions, action)
	}
	return plan, nil
}

// Execute converges each play in scope, in declared order. FAIL LOUD: any
// failed or unreachable host fails that play's action — there is no silent
// partial converge.
func (b *Backend) Execute(ctx context.Context, plan *runtime.LifecyclePlan, rctx *runtime.RuntimeContext) (*runtime.LifecycleResult, error) {
	// Results reset so a dry-run check pass never mixes with apply results.
	b.results = nil

	var results []runtime.ActionResult
	for _, p := range b.plays {
		start := time.Now()
		res := b.runPlay(ctx, b.cfg, rctx, p, false)
		b.results = append(b.results, res)

		ar := runtime.ActionResult{Name: p.ID, Duration: time.Since(start)}
		agg := AggregateResults([]PlayResult{res})
		failed := res.FailedHosts()
		switch {
		case len(res.Hosts) == 0:
			ar.Success = false
			ar.Message = fmt.Sprintf("no recap parsed (exit %d) — run output unreadable", res.ExitCode)
			ar.Stderr = tail(res.Output, 2000)
		case len(failed) > 0 || res.ExitCode != 0:
			ar.Success = false
			ar.Message = fmt.Sprintf("%d/%d hosts failed or unreachable: %s", len(failed), agg.Total, strings.Join(failed, ", "))
			ar.Stderr = tail(res.Output, 2000)
		default:
			ar.Success = true
			ar.Message = fmt.Sprintf("converged %d/%d hosts (%d changed)", agg.Converged, agg.Total, agg.Changed)
		}
		results = append(results, ar)
	}
	return &runtime.LifecycleResult{Actions: results}, nil
}

func (b *Backend) Cleanup(rctx *runtime.RuntimeContext) {
	// Key tmpfile removal is registered via rctx.Resolved.AddCleanup in Prepare.
}

// runPlay executes one playbook inside the execution image and parses its recap.
func (b *Backend) runPlay(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext, p config.AnsiblePlaybook, check bool) PlayResult {
	a := cfg.Ansible
	args := []string{
		"run", "--rm",
		"-v", rctx.RepoRoot + ":" + containerWorkdir,
		"-w", containerWorkdir,
		"-v", b.keyPath + ":" + containerKeyPath + ":ro",
		"-v", filepath.Join(rctx.RepoRoot, a.SSH.KnownHosts) + ":" + containerKnownHosts + ":ro",
		"-e", "ANSIBLE_HOST_KEY_CHECKING=True",
		"-e", "ANSIBLE_SSH_COMMON_ARGS=-o UserKnownHostsFile=" + containerKnownHosts + " -o StrictHostKeyChecking=yes",
		"-e", "ANSIBLE_FORCE_COLOR=0",
		a.Image,
		"ansible-playbook",
		"-i", a.Inventory,
		p.Path,
		"--limit", strings.Join(p.Groups, ":"),
		"-u", a.SSH.User,
		"--private-key", containerKeyPath,
	}
	if check {
		args = append(args, "--check", "--diff")
	}
	for _, ev := range b.extraVars {
		args = append(args, "-e", ev)
	}

	cmd := exec.CommandContext(ctx, b.dockerBin, args...)
	out, err := cmd.CombinedOutput()
	res := PlayResult{ID: p.ID, Path: p.Path, Check: check, Output: string(out)}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			res.Output += "\n" + err.Error()
		}
	}
	res.Hosts = ParseRecap(res.Output)
	return res
}

// docker runs the docker CLI and returns combined output.
func (b *Backend) docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, b.dockerBin, args...).CombinedOutput()
	return string(out), err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
