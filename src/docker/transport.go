package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// LocalTransport executes commands directly on the local host.
type LocalTransport struct{}

func (l *LocalTransport) Exec(ctx context.Context, cmd string, args ...string) ([]byte, []byte, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (l *LocalTransport) CopyTo(_ context.Context, localPath, remotePath string) error {
	// Local: just a file copy
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	return os.WriteFile(remotePath, data, 0600)
}

func (l *LocalTransport) Close() error { return nil }

// SSHTransport executes commands on a remote host via SSH.
// Always exec form — no shell string concatenation.
type SSHTransport struct {
	Host    string // hostname or IP
	User    string // SSH user (default: current user)
	KeyPath string // optional SSH key path
}

func (s *SSHTransport) Exec(ctx context.Context, cmd string, args ...string) ([]byte, []byte, error) {
	sshArgs := s.baseArgs()
	// Build remote command in exec form: ssh host -- cmd arg1 arg2
	sshArgs = append(sshArgs, "--")
	sshArgs = append(sshArgs, cmd)
	sshArgs = append(sshArgs, args...)

	c := exec.CommandContext(ctx, "ssh", sshArgs...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (s *SSHTransport) CopyTo(ctx context.Context, localPath, remotePath string) error {
	scpArgs := []string{}
	if s.KeyPath != "" {
		scpArgs = append(scpArgs, "-i", s.KeyPath)
	}
	scpArgs = append(scpArgs, "-o", "StrictHostKeyChecking=accept-new")
	scpArgs = append(scpArgs, "-o", "BatchMode=yes")

	target := remotePath
	if s.User != "" {
		target = s.User + "@" + s.Host + ":" + remotePath
	} else {
		target = s.Host + ":" + remotePath
	}
	scpArgs = append(scpArgs, localPath, target)

	cmd := exec.CommandContext(ctx, "scp", scpArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("scp to %s:%s: %s", s.Host, remotePath, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *SSHTransport) Close() error { return nil }

func (s *SSHTransport) baseArgs() []string {
	args := []string{}
	if s.KeyPath != "" {
		args = append(args, "-i", s.KeyPath)
	}
	args = append(args, "-o", "StrictHostKeyChecking=accept-new")
	args = append(args, "-o", "BatchMode=yes")
	args = append(args, "-o", "ConnectTimeout=10")

	if s.User != "" {
		args = append(args, s.User+"@"+s.Host)
	} else {
		args = append(args, s.Host)
	}
	return args
}

// ResolveTransport creates the appropriate transport for a host target.
// Local if address matches local indicators, SSH otherwise.
func ResolveTransport(target HostTarget) HostTransport {
	// Check for explicit local flag in host vars
	if target.Vars["docker_local"] == "true" {
		return &LocalTransport{}
	}

	// Default: SSH
	user := target.Vars["ansible_user"]
	keyPath := target.Vars["ansible_ssh_private_key_file"]
	addr := target.Address
	if addr == "" {
		addr = target.Name
	}

	return &SSHTransport{
		Host:    addr,
		User:    user,
		KeyPath: keyPath,
	}
}
