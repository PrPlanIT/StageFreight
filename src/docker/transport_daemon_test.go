package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runtime"
)

func withFakeBin(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A host scope uses its transport; a group scope uses the runner's daemon, or is refused.
func TestResolveTransportForStack(t *testing.T) {
	ssh := &SSHTransport{Host: "10.0.0.9"}
	targets := []HostTarget{{Name: "lonely-box", Transport: ssh}}

	for _, daemon := range []bool{true, false} {
		c := &ComposeBackend{targets: targets, daemon: func() bool { return daemon }}
		got, err := c.resolveTransportForStack(DockerPlanMeta{Scope: "lonely-box"})
		if err != nil || got != ssh {
			t.Errorf("declared host (daemon=%v): %v %v", daemon, got, err)
		}
	}

	withDaemon := &ComposeBackend{targets: targets, daemon: func() bool { return true }}
	got, err := withDaemon.resolveTransportForStack(DockerPlanMeta{Scope: "web"})
	if _, local := got.(*LocalTransport); err != nil || !local {
		t.Errorf("group scope on a docker runner falls back to the local daemon: %T %v", got, err)
	}

	daemonless := &ComposeBackend{targets: targets, daemon: func() bool { return false }}
	got, err = daemonless.resolveTransportForStack(DockerPlanMeta{Scope: "web"})
	if got != nil || err == nil || !strings.Contains(err.Error(), `"web" is not a declared target host`) || !strings.Contains(err.Error(), "no Docker daemon") {
		t.Errorf("group scope on a daemonless runner must refuse: %v %v", got, err)
	}
}

const localTargetInventory = `all:
  hosts:
    box:
      ansible_host: 10.0.0.9
      docker_local: "true"
    far:
      ansible_host: 10.0.0.10
docker_hosts:
  hosts:
    box:
    far:
`

// A docker_local target is refused at Prepare without a daemon; SSH targets need none.
func TestPrepare_LocalTargetNeedsDaemon(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "inventory.yml"), []byte(localTargetInventory), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgFor := func(groups ...string) *config.Config {
		cfg := &config.Config{}
		cfg.Docker.Targets.Inventory = "inventory.yml"
		cfg.Docker.Targets.Selector.Groups = groups
		return cfg
	}
	rctx := func() *runtime.RuntimeContext { return &runtime.RuntimeContext{RepoRoot: repo} }

	c := &ComposeBackend{daemon: func() bool { return false }}
	err := c.Prepare(context.Background(), cfgFor("docker_hosts"), rctx())
	if err == nil || !strings.Contains(err.Error(), "target box is local") {
		t.Errorf("daemonless runner with a local target: %v", err)
	}

	c = &ComposeBackend{daemon: func() bool { return true }}
	if err := c.Prepare(context.Background(), cfgFor("docker_hosts"), rctx()); err != nil {
		t.Errorf("docker runner with a local target: %v", err)
	}
}

// A failed `docker ps` is an error on both transports, never an empty stack.
func TestInspectStack_ListFailureIsAnError(t *testing.T) {
	withFakeBin(t, "docker", "#!/bin/sh\necho 'Cannot connect to the Docker daemon' >&2\nexit 1\n")
	if _, err := (&LocalTransport{}).InspectStack(context.Background(), "app"); err == nil || !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Errorf("local transport: %v", err)
	}

	withFakeBin(t, "ssh", "#!/bin/sh\necho 'permission denied' >&2\nexit 255\n")
	if _, err := (&SSHTransport{Host: "10.0.0.9"}).InspectStack(context.Background(), "app"); err == nil || !strings.Contains(err.Error(), "on 10.0.0.9: permission denied") {
		t.Errorf("ssh transport: %v", err)
	}
}

// A successful `docker ps` with no containers is an empty stack on both transports.
func TestInspectStack_NothingRunning(t *testing.T) {
	withFakeBin(t, "docker", "#!/bin/sh\nexit 0\n")
	if got, err := (&LocalTransport{}).InspectStack(context.Background(), "app"); err != nil || got.Project != "app" {
		t.Errorf("local transport: %+v %v", got, err)
	}

	withFakeBin(t, "ssh", "#!/bin/sh\nexit 0\n")
	if got, err := (&SSHTransport{Host: "10.0.0.9"}).InspectStack(context.Background(), "app"); err != nil || got.Project != "app" {
		t.Errorf("ssh transport: %+v %v", got, err)
	}
}
