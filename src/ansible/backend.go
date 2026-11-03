// Package ansible implements the host-convergence lifecycle backend: declared
// playbooks executed against SSH-managed hosts, in the declared execution image or
// by the job's own ansible. The package returns DATA — plans, recaps, aggregates; all terminal rendering
// belongs to cli/cmd.
package ansible

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
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

// Backend implements runtime.LifecycleBackend for ansible.
//
// Scope defaults to the config's converge set (perform reconcile). SelectPlay
// narrows it to one named entry — the `ansible run <id>` path, which is also
// the ONLY way a converge:false runbook ever executes.
type Backend struct {
	cfg         *config.Config   // captured at Validate; Execute composes the play from it
	probe       func() substrate // job-environment probe; nil = probeSubstrate
	runtime     Runtime
	dockerBin   string // image runtime: the docker CLI
	nativeBin   string // native runtime: ansible-playbook in the job environment
	nativeID    string // native runtime: the ansible-playbook identity recorded at Validate
	imageRef    string // config reference
	imageID     string // resolved digest/ID recorded at Validate (image runtime)
	keyMaterial []byte // (decrypted) private key, held in process memory only
	plays       []config.AnsiblePlaybook
	extraVars   []string // -e key=val passthrough (run verb)
	results     []PlayResult
	stream      func(playID string) io.WriteCloser // live-output sink factory (cmd-owned)
}

// SetStream supplies a per-play live-output sink factory. The backend tees
// the play's raw subprocess output into it as it runs (pure passthrough —
// presentation, section markers, and destination belong to the caller) while
// still capturing the full text for recap parsing. Nil disables streaming.
func (b *Backend) SetStream(f func(playID string) io.WriteCloser) { b.stream = f }

// SelectPlay narrows execution to a single declared play (the run verb).
func (b *Backend) SelectPlay(p config.AnsiblePlaybook) { b.plays = []config.AnsiblePlaybook{p} }

// SetExtraVars supplies launch-time -e key=val pairs (the run verb).
func (b *Backend) SetExtraVars(vars []string) { b.extraVars = vars }

// PlayResults returns the structured per-play outcomes after Execute (or the
// check results after a dry-run Plan).
func (b *Backend) PlayResults() []PlayResult { return b.results }

// Aggregate returns the unique-host rollup feeding the ansible.* facts.
func (b *Backend) Aggregate() Aggregate { return AggregateResults(b.results) }

// Runtime returns where the plays run, resolved at Validate.
func (b *Backend) Runtime() Runtime { return b.runtime }

// ImageRef returns the execution image reference (for trust display); empty for a native run.
func (b *Backend) ImageRef() string {
	if b.runtime == RuntimeNative {
		return ""
	}
	return b.imageRef
}

// NativeIdentity returns the native ansible-playbook's path and version.
func (b *Backend) NativeIdentity() string { return b.nativeID }

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

// Validate resolves where the plays run, records that runtime's identity, and
// checks the declared files.
func (b *Backend) Validate(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext) error {
	b.cfg = cfg
	a := cfg.Ansible
	if len(b.plays) == 0 {
		b.plays = a.ConvergePlaybooks()
	}
	if len(b.plays) == 0 {
		return fmt.Errorf("no ansible playbooks in scope")
	}
	b.imageRef = a.Image

	probe := b.probe
	if probe == nil {
		probe = probeSubstrate
	}
	sub := probe()
	rt, err := selectRuntime(sub, "ansible-playbook", a.Image)
	if err != nil {
		return err
	}
	b.runtime = rt

	switch rt {
	case RuntimeImage:
		b.dockerBin = sub.dockerBin
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
	case RuntimeNative:
		for _, tool := range []string{"ssh-agent", "ssh-add"} {
			if sub.look(tool) == "" {
				return fmt.Errorf("ansible-playbook runs natively here, and holding the SSH key in memory needs %s, which this job's environment does not have", tool)
			}
		}
		b.nativeBin = sub.look("ansible-playbook")
		b.nativeID = toolIdentity(ctx, b.nativeBin)
	}

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
// in process memory — no tmpfile; it reaches each play container over stdin,
// or a native play's private ssh-agent.
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

// runPlay executes one playbook in the resolved runtime and parses its recap.
func (b *Backend) runPlay(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext, p config.AnsiblePlaybook, check bool) PlayResult {
	res := PlayResult{ID: p.ID, Path: p.Path, Check: check}

	// Tee, don't buffer: a converge runs for many minutes per node, and a
	// silent log is indistinguishable from a hung one. The capture buffer
	// stays authoritative for recap parsing.
	var buf bytes.Buffer
	dest := io.Writer(&buf)
	var sink io.WriteCloser
	if b.stream != nil {
		sink = b.stream(p.ID)
		dest = io.MultiWriter(&buf, sink)
	}

	var code int
	var err error
	if b.runtime == RuntimeNative {
		code, err = b.runPlayNative(ctx, cfg, rctx, p, check, dest)
	} else {
		code, err = b.runPlayImage(ctx, cfg, rctx, p, check, dest)
	}
	if sink != nil {
		sink.Close()
	}
	res.Output = buf.String()
	res.ExitCode = code
	if err != nil {
		res.ExitCode = -1
		res.Output += err.Error()
		return res
	}
	res.Hosts = ParseRecap(res.Output)
	return res
}

// playArgs is the ansible-playbook argv for one play; keyPath is empty when an
// ssh-agent holds the key.
func (b *Backend) playArgs(a config.AnsibleConfig, p config.AnsiblePlaybook, check bool, keyPath string) []string {
	args := []string{
		"-i", a.Inventory,
		p.Path,
		"--limit", strings.Join(p.Groups, ":"),
		"-u", a.SSH.User,
	}
	if keyPath != "" {
		args = append(args, "--private-key", keyPath)
	}
	if check {
		args = append(args, "--check", "--diff")
	}
	for _, ev := range b.extraVars {
		args = append(args, "-e", ev)
	}
	return args
}

// playEnv is the ansible environment for a play: strict host-key checking
// against the committed known_hosts file.
func playEnv(knownHosts string) []string {
	return []string{
		"ANSIBLE_HOST_KEY_CHECKING=True",
		"ANSIBLE_SSH_COMMON_ARGS=-o UserKnownHostsFile=" + knownHosts + " -o StrictHostKeyChecking=yes",
		"ANSIBLE_FORCE_COLOR=0",
	}
}

// runPlayImage runs the play in the execution image; the key is piped onto a tmpfs.
func (b *Backend) runPlayImage(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext, p config.AnsiblePlaybook, check bool, out io.Writer) (int, error) {
	a := cfg.Ansible
	playCmd := append([]string{"ansible-playbook"}, b.playArgs(a, p, check, containerKeyPath)...)
	// The key arrives on stdin before the workspace cd — cat's EOF is the
	// signal the material is fully landed on the tmpfs.
	script := "umask 077 && cat > " + containerKeyPath +
		" && cd " + containerWorkdir +
		" && exec " + shellJoin(playCmd)

	flags := []string{"--tmpfs", containerSecretDir + ":rw,mode=0700"}
	for _, e := range playEnv(path.Join(containerWorkdir, a.SSH.KnownHosts)) {
		flags = append(flags, "-e", e)
	}
	return imageRun{
		dockerBin:   b.dockerBin,
		image:       a.Image,
		workspace:   rctx.RepoRoot,
		createFlags: flags,
		script:      script,
		stdin:       bytes.NewReader(b.keyMaterial),
		stdout:      out,
		stderr:      out,
	}.run(ctx)
}

// runPlayNative runs the play with the job's own ansible-playbook and a private ssh-agent.
func (b *Backend) runPlayNative(ctx context.Context, cfg *config.Config, rctx *runtime.RuntimeContext, p config.AnsiblePlaybook, check bool, out io.Writer) (int, error) {
	a := cfg.Ansible
	agent, err := startKeyAgent(ctx, b.keyMaterial)
	if err != nil {
		return -1, err
	}
	defer agent.stop()

	env := append(playEnv(filepath.Join(rctx.RepoRoot, a.SSH.KnownHosts)), "SSH_AUTH_SOCK="+agent.sock)
	return nativeRun{
		bin:       b.nativeBin,
		args:      b.playArgs(a, p, check, ""),
		workspace: rctx.RepoRoot,
		env:       env,
		stdout:    out,
		stderr:    out,
	}.run(ctx)
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
