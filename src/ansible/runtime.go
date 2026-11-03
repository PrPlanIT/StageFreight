package ansible

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/runner"
)

// Runtime is where an ansible tool (ansible-playbook, ansible-lint) executes.
type Runtime string

const (
	// RuntimeImage runs the tool inside the declared execution image on a Docker daemon.
	RuntimeImage Runtime = "image"

	// RuntimeNative runs the tool from the job's own environment (no daemon needed).
	RuntimeNative Runtime = "native"
)

// substrate is what the job environment offers ansible.
type substrate struct {
	daemon    bool                     // a Docker daemon answers
	dockerBin string                   // docker CLI path when the daemon answers
	look      func(tool string) string // a tool's absolute path in the job environment, "" when absent
}

// probeSubstrate reads the job environment.
func probeSubstrate() substrate {
	s := substrate{look: lookPath}
	if _, ok := runner.DockerDaemon(); ok {
		if bin := lookPath("docker"); bin != "" {
			s.daemon, s.dockerBin = true, bin
		}
	}
	return s
}

func lookPath(tool string) string {
	bin, err := exec.LookPath(tool)
	if err != nil {
		return ""
	}
	return bin
}

// selectRuntime prefers the execution image whenever a daemon can run it, then the
// job's own tool; with neither it refuses.
func selectRuntime(s substrate, tool, image string) (Runtime, error) {
	switch {
	case s.daemon:
		return RuntimeImage, nil
	case s.look(tool) != "":
		return RuntimeNative, nil
	default:
		return "", fmt.Errorf("%s has nowhere to run: no Docker daemon to run the execution image %s, and no %s in this job's environment — run it on a Docker runner, or in a job image that ships ansible", tool, image, tool)
	}
}

// ConvergeNeedsDocker reports whether host convergence needs the execution image:
// true when the job environment has no ansible-playbook of its own.
func ConvergeNeedsDocker() bool {
	return lookPath("ansible-playbook") == ""
}

// Container-side paths. The workspace is copied in, never bind-mounted: a bind
// mount resolves on the daemon's filesystem. The key lives on a container-private tmpfs.
const (
	containerWorkdir   = "/work"
	containerSecretDir = "/run/sf"
	containerKeyPath   = containerSecretDir + "/key"
)

// imageRun is one command in the execution image on a Docker daemon (create, cp, start).
type imageRun struct {
	dockerBin   string
	image       string
	workspace   string    // host directory copied to /work
	createFlags []string  // extra `docker create` flags (tmpfs, -e)
	script      string    // /bin/sh -c script; runs with the workspace landed at /work
	stdin       io.Reader // nil = no stdin
	stdout      io.Writer
	stderr      io.Writer
}

// run executes the command and returns its exit code. A non-nil error means the
// container could not be set up (create or copy failed); the command never ran.
func (r imageRun) run(ctx context.Context) (int, error) {
	args := []string{"create"}
	if r.stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, r.createFlags...)
	args = append(args, "--entrypoint", "/bin/sh", r.image, "-c", r.script)
	out, err := exec.CommandContext(ctx, r.dockerBin, args...).CombinedOutput()
	if err != nil {
		return -1, fmt.Errorf("container create failed: %s", strings.TrimSpace(string(out)))
	}
	cid := strings.TrimSpace(string(out))
	defer exec.CommandContext(context.WithoutCancel(ctx), r.dockerBin, "rm", "-f", cid).Run()

	// The destination must not pre-exist (docker cp copies contents into a new dir),
	// so the container has no -w and the script cd's instead.
	if out, err := exec.CommandContext(ctx, r.dockerBin, "cp", r.workspace, cid+":"+containerWorkdir).CombinedOutput(); err != nil {
		return -1, fmt.Errorf("workspace copy failed: %s", strings.TrimSpace(string(out)))
	}

	start := []string{"start", "-a"}
	if r.stdin != nil {
		start = append(start, "-i")
	}
	cmd := exec.CommandContext(ctx, r.dockerBin, append(start, cid)...)
	cmd.Stdin = r.stdin
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	return exitCode(cmd.Run())
}

// nativeRun is one command from the job's own environment, run in the workspace.
type nativeRun struct {
	bin       string
	args      []string
	workspace string
	env       []string // added to the job environment
	stdout    io.Writer
	stderr    io.Writer
}

// run executes the command and returns its exit code. A non-nil error means the
// command could not be started.
func (r nativeRun) run(ctx context.Context) (int, error) {
	cmd := exec.CommandContext(ctx, r.bin, r.args...)
	cmd.Dir = r.workspace
	cmd.Env = append(os.Environ(), r.env...)
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	return exitCode(cmd.Run())
}

// exitCode maps a finished command's error to its exit code. Only a command that
// never started is an error.
func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), nil
	}
	return -1, err
}

// keyAgent holds a native run's SSH key in an ssh-agent's memory; it never touches disk.
type keyAgent struct {
	cmd  *exec.Cmd
	dir  string
	sock string
}

// startKeyAgent starts a private ssh-agent and loads key into it over stdin.
func startKeyAgent(ctx context.Context, key []byte) (*keyAgent, error) {
	dir, err := os.MkdirTemp("", "sf-ansible-agent-*")
	if err != nil {
		return nil, fmt.Errorf("ssh-agent socket dir: %w", err)
	}
	k := &keyAgent{dir: dir, sock: filepath.Join(dir, "agent.sock")}
	k.cmd = exec.CommandContext(ctx, "ssh-agent", "-D", "-a", k.sock)
	var agentOut bytes.Buffer
	k.cmd.Stdout = &agentOut
	k.cmd.Stderr = &agentOut
	if err := k.cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("starting ssh-agent: %w", err)
	}
	if err := waitForPath(k.sock, 5*time.Second); err != nil {
		k.stop()
		return nil, fmt.Errorf("ssh-agent did not open its socket: %s", strings.TrimSpace(agentOut.String()))
	}

	add := exec.CommandContext(ctx, "ssh-add", "-q", "-")
	add.Env = append(os.Environ(), "SSH_AUTH_SOCK="+k.sock)
	add.Stdin = bytes.NewReader(key)
	if out, err := add.CombinedOutput(); err != nil {
		k.stop()
		return nil, fmt.Errorf("loading the key into ssh-agent: %s", strings.TrimSpace(string(out)))
	}
	return k, nil
}

// stop ends the agent (dropping the key with it) and removes its socket dir.
func (k *keyAgent) stop() {
	if k.cmd.Process != nil {
		k.cmd.Process.Kill()
		k.cmd.Wait()
	}
	os.RemoveAll(k.dir)
}

func waitForPath(p string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not appear within %s", p, timeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// toolIdentity is the first line of `<bin> --version`, recorded in place of an image digest.
func toolIdentity(ctx context.Context, bin string) string {
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return bin
	}
	return bin + " (" + firstLine(string(out)) + ")"
}
