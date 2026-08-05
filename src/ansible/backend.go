// Package ansible implements the host-convergence lifecycle backend: declared
// playbooks executed inside a containerized ansible runtime (the execution
// image) against SSH-managed hosts. The package returns DATA — plans, recaps,
// aggregates; all terminal rendering belongs to cli/cmd.
package ansible

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path"
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

// Container-side paths. The workspace is COPIED in (docker cp — works against
// any docker endpoint, local socket or remote daemon, where bind mounts
// resolve on the daemon's filesystem and silently come up empty). The key
// lives on a container-private tmpfs, streamed over stdin at start — it never
// touches a filesystem outside the container's RAM.
const (
	containerWorkdir   = "/work"
	containerSecretDir = "/run/sf"
	containerKeyPath   = containerSecretDir + "/key"
)

// Backend implements runtime.LifecycleBackend for containerized ansible.
//
// Scope defaults to the config's converge set (perform reconcile). SelectPlay
// narrows it to one named entry — the `ansible run <id>` path, which is also
// the ONLY way a converge:false runbook ever executes.
type Backend struct {
	cfg         *config.Config // captured at Validate; Execute composes container args from it
	dockerBin   string
	imageRef    string // config reference
	imageID     string // resolved digest/ID recorded at Validate
	keyMaterial []byte // (decrypted) private key, held in process memory only
	plays       []config.AnsiblePlaybook
	extraVars   []string // -e key=val passthrough (run verb)
	results     []PlayResult
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
// available in the environment — raw (<PREFIX>_SSH_KEY) or base64
// (<PREFIX>_SSH_KEY_B64, the maskable single-line form, mirroring the gitops
// <NAME>_CA_B64 convention). The key ships as a forge-protected variable, so
// MR/unprotected-ref pipelines legitimately lack it — dispatch uses this for a
// clear "skipped" instead of a mid-run auth failure.
func CredentialsPresent(cfg *config.Config) bool {
	prefix := cfg.Ansible.SSH.EnvPrefix()
	return os.Getenv(prefix+"_SSH_KEY") != "" || os.Getenv(prefix+"_SSH_KEY_B64") != ""
}

// resolveKeyMaterial reads the private key from the environment: the raw PEM
// variable wins; the _B64 variant decodes (forge masking rejects multiline
// values, so the base64 form is what a masked variable stores).
func resolveKeyMaterial(prefix string) ([]byte, error) {
	if raw := os.Getenv(prefix + "_SSH_KEY"); raw != "" {
		return []byte(raw), nil
	}
	if b64 := os.Getenv(prefix + "_SSH_KEY_B64"); b64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if err != nil {
			return nil, fmt.Errorf("%s_SSH_KEY_B64: decoding: %w", prefix, err)
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("%s_SSH_KEY (or %s_SSH_KEY_B64) not set — ansible credentials unavailable in this context", prefix, prefix)
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

// Prepare resolves the SSH identity: the key from <PREFIX>_SSH_KEY
// (decrypted via <PREFIX>_SSH_KEY_PASSPHRASE when set) is validated and held
// in process memory — no tmpfile; it reaches each play container over stdin.
// Also validates every play's groups against the inventory so an empty
// --limit fails here, not silently inside ansible.
func (b *Backend) Prepare(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	a := cfg.Ansible
	prefix := a.SSH.EnvPrefix()
	keyBytes, err := resolveKeyMaterial(prefix)
	if err != nil {
		return err
	}
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
	// PEM requires a trailing newline and shell plumbing loves to strip it
	// ($(cat key) does, CI variable UIs sometimes do) — normalize, since
	// OpenSSH rejects the un-terminated form as "invalid format".
	if len(keyBytes) > 0 && keyBytes[len(keyBytes)-1] != '\n' {
		keyBytes = append(keyBytes, '\n')
	}

	b.keyMaterial = keyBytes

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

// runPlay executes one playbook inside the execution image and parses its
// recap. Three-step container flow — create, cp, start — so the workspace is
// a per-run copy streamed over the docker API (endpoint-agnostic: a remote
// daemon never sees this filesystem, so bind mounts are off the table) and
// the key is piped over stdin onto a container-private tmpfs.
func (b *Backend) runPlay(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext, p config.AnsiblePlaybook, check bool) PlayResult {
	a := cfg.Ansible
	res := PlayResult{ID: p.ID, Path: p.Path, Check: check}

	playCmd := []string{
		"ansible-playbook",
		"-i", a.Inventory,
		p.Path,
		"--limit", strings.Join(p.Groups, ":"),
		"-u", a.SSH.User,
		"--private-key", containerKeyPath,
	}
	if check {
		playCmd = append(playCmd, "--check", "--diff")
	}
	for _, ev := range b.extraVars {
		playCmd = append(playCmd, "-e", ev)
	}
	// The key arrives on stdin before the workspace cd — cat's EOF is the
	// signal the material is fully landed on the tmpfs.
	script := "umask 077 && cat > " + containerKeyPath +
		" && cd " + containerWorkdir +
		" && exec " + shellJoin(playCmd)

	createArgs := []string{
		"create", "-i",
		"--tmpfs", containerSecretDir + ":rw,mode=0700",
		"-e", "ANSIBLE_HOST_KEY_CHECKING=True",
		"-e", "ANSIBLE_SSH_COMMON_ARGS=-o UserKnownHostsFile=" + path.Join(containerWorkdir, a.SSH.KnownHosts) + " -o StrictHostKeyChecking=yes",
		"-e", "ANSIBLE_FORCE_COLOR=0",
		"--entrypoint", "/bin/sh",
		a.Image, "-c", script,
	}
	out, err := b.docker(ctx, createArgs...)
	if err != nil {
		res.ExitCode = -1
		res.Output = "container create failed: " + strings.TrimSpace(out)
		return res
	}
	cid := strings.TrimSpace(out)
	defer b.docker(context.WithoutCancel(ctx), "rm", "-f", cid)

	// Workspace copy. The destination must NOT pre-exist so docker cp applies
	// its contents-into-new-directory semantics (that's why the container has
	// no -w and the script cd's instead).
	if out, err := b.docker(ctx, "cp", rctx.RepoRoot, cid+":"+containerWorkdir); err != nil {
		res.ExitCode = -1
		res.Output = "workspace copy failed: " + strings.TrimSpace(out)
		return res
	}

	cmd := exec.CommandContext(ctx, b.dockerBin, "start", "-a", "-i", cid)
	cmd.Stdin = bytes.NewReader(b.keyMaterial)
	runOut, runErr := cmd.CombinedOutput()
	res.Output = string(runOut)
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
			res.Output += "\n" + runErr.Error()
		}
	}
	res.Hosts = ParseRecap(res.Output)
	return res
}

// shellJoin renders argv as a single-quoted /bin/sh command string.
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
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
