package ansible

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/runtime"
)

// Fakes log their argv and environment under FAKE_DIR.

const recapOutput = `PLAY [converge] *****
TASK [ping] *****
ok: [host1]

PLAY RECAP *********************************************************************
host1                      : ok=3    changed=1    unreachable=0    failed=0    skipped=0    rescued=0    ignored=0
`

// fakeDocker answers the image runtime's docker calls; `start` records stdin.
const fakeDocker = `#!/bin/sh
echo "$*" >> "$FAKE_DIR/docker.log"
case "$1" in
create) echo cid123 ;;
start) cat > "$FAKE_DIR/stdin"; printf '%s' "$FAKE_STDOUT"; printf '%s' "$FAKE_STDERR" >&2; exit ${FAKE_EXIT:-0} ;;
image) echo sha256:feedface ;;
esac
`

// fakeTool stands in for ansible-playbook / ansible-lint in the job environment.
const fakeTool = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "ansible-playbook [core 2.17.4]"; echo "  config file = None"; exit 0; fi
echo "$*" >> "$FAKE_DIR/$(basename "$0").log"
pwd > "$FAKE_DIR/$(basename "$0").cwd"
echo "$SSH_AUTH_SOCK" > "$FAKE_DIR/$(basename "$0").sock"
printf '%s' "$FAKE_STDOUT"; printf '%s' "$FAKE_STDERR" >&2
exit ${FAKE_EXIT:-0}
`

// fakeAgent opens its socket path (-D -a <sock>) and waits to be killed.
const fakeAgent = `#!/bin/sh
touch "$3"
echo "$3" > "$FAKE_DIR/agent.sock"
exec sleep 60
`

// fakeAdd records the key it is fed and the agent it was pointed at.
const fakeAdd = `#!/bin/sh
cat > "$FAKE_DIR/added-key"
echo "$SSH_AUTH_SOCK" > "$FAKE_DIR/add.sock"
`

// fakeBins writes the named fakes into a fresh bin dir and returns it.
func fakeBins(t *testing.T, bins map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range bins {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_DIR", dir)
	return dir
}

func lookIn(dir string) func(string) string {
	return func(tool string) string {
		p := filepath.Join(dir, tool)
		if _, err := os.Stat(p); err != nil {
			return ""
		}
		return p
	}
}

func readFake(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// The image runs where a daemon answers, the job's own tool otherwise, and neither refuses.
func TestSelectRuntime(t *testing.T) {
	native := func(string) string { return "/usr/bin/ansible-playbook" }
	none := func(string) string { return "" }
	cases := []struct {
		name string
		sub  substrate
		want Runtime
	}{
		{"docker runner", substrate{daemon: true, look: none}, RuntimeImage},
		{"docker runner whose job also ships ansible", substrate{daemon: true, look: native}, RuntimeImage},
		{"daemonless runner with ansible", substrate{look: native}, RuntimeNative},
	}
	for _, tc := range cases {
		got, err := selectRuntime(tc.sub, "ansible-playbook", "docker.io/hlhd/ansible:v1")
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}

	_, err := selectRuntime(substrate{look: none}, "ansible-playbook", "docker.io/hlhd/ansible:v1")
	if err == nil {
		t.Fatal("daemonless runner without ansible must refuse")
	}
	for _, want := range []string{"no Docker daemon", "docker.io/hlhd/ansible:v1", "Docker runner", "ships ansible"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q must mention %q", err, want)
		}
	}
}

// Converge needs Docker only when the job has no ansible-playbook.
func TestConvergeNeedsDocker(t *testing.T) {
	bin := fakeBins(t, map[string]string{"ansible-playbook": fakeTool})
	t.Setenv("PATH", bin)
	if ConvergeNeedsDocker() {
		t.Error("a job that ships ansible-playbook must not need Docker")
	}
	t.Setenv("PATH", t.TempDir())
	if !ConvergeNeedsDocker() {
		t.Error("a job without ansible-playbook can only run the execution image: it needs Docker")
	}
}

func convergeFixture(t *testing.T) (*config.Config, *runtime.RuntimeContext) {
	t.Helper()
	repo := t.TempDir()
	for _, f := range []string{"inventory.yml", "site.yml", "known_hosts"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Ansible: config.AnsibleConfig{
		Image:     "docker.io/hlhd/ansible:v1",
		Inventory: "inventory.yml",
		SSH:       config.AnsibleSSH{User: "deploy", KnownHosts: "known_hosts"},
		Playbooks: config.OrderedAnsiblePlaybooks{{ID: "site", Path: "site.yml", Groups: []string{"web", "db"}, Converge: true}},
	}}
	return cfg, &runtime.RuntimeContext{RepoRoot: repo}
}

// A Docker runner resolves and records the image; no native identity.
func TestValidate_DockerRunner(t *testing.T) {
	bin := fakeBins(t, map[string]string{"docker": fakeDocker})
	cfg, rctx := convergeFixture(t)
	b := &Backend{probe: func() substrate {
		return substrate{daemon: true, dockerBin: filepath.Join(bin, "docker"), look: lookIn(bin)}
	}}
	if err := b.Validate(context.Background(), cfg, rctx); err != nil {
		t.Fatal(err)
	}
	if b.Runtime() != RuntimeImage || b.ImageRef() != "docker.io/hlhd/ansible:v1" || b.ImageID() != "sha256:feedface" {
		t.Errorf("runtime=%q image=%q id=%q", b.Runtime(), b.ImageRef(), b.ImageID())
	}
	if b.NativeIdentity() != "" {
		t.Errorf("image runtime must not claim a native identity: %q", b.NativeIdentity())
	}
}

// A daemonless runner records the native ansible-playbook; no image identity.
func TestValidate_DaemonlessRunner(t *testing.T) {
	bin := fakeBins(t, map[string]string{"ansible-playbook": fakeTool, "ssh-agent": fakeAgent, "ssh-add": fakeAdd})
	cfg, rctx := convergeFixture(t)
	b := &Backend{probe: func() substrate { return substrate{look: lookIn(bin)} }}
	if err := b.Validate(context.Background(), cfg, rctx); err != nil {
		t.Fatal(err)
	}
	if b.Runtime() != RuntimeNative {
		t.Fatalf("runtime = %q", b.Runtime())
	}
	if b.ImageRef() != "" {
		t.Errorf("native runtime must not report the image as what ran: %q", b.ImageRef())
	}
	if id := b.NativeIdentity(); !strings.Contains(id, "ansible-playbook [core 2.17.4]") || !strings.HasPrefix(id, bin) {
		t.Errorf("native identity = %q", id)
	}
}

// Validate refuses with no runtime, or a native runtime without ssh-agent/ssh-add.
func TestValidate_DaemonlessRunnerRefusals(t *testing.T) {
	cfg, rctx := convergeFixture(t)

	empty := fakeBins(t, nil)
	b := &Backend{probe: func() substrate { return substrate{look: lookIn(empty)} }}
	if err := b.Validate(context.Background(), cfg, rctx); err == nil || !strings.Contains(err.Error(), "nowhere to run") {
		t.Errorf("no runtime: %v", err)
	}

	noAgent := fakeBins(t, map[string]string{"ansible-playbook": fakeTool})
	b = &Backend{probe: func() substrate { return substrate{look: lookIn(noAgent)} }}
	if err := b.Validate(context.Background(), cfg, rctx); err == nil || !strings.Contains(err.Error(), "ssh-agent") {
		t.Errorf("no ssh-agent: %v", err)
	}
}

// The image run copies the workspace, streams the key over stdin and removes the container.
func TestRunPlay_DockerRunner(t *testing.T) {
	bin := fakeBins(t, map[string]string{"docker": fakeDocker})
	t.Setenv("FAKE_STDOUT", recapOutput)
	cfg, rctx := convergeFixture(t)
	b := &Backend{runtime: RuntimeImage, dockerBin: filepath.Join(bin, "docker"), keyMaterial: []byte("KEY\n")}

	res := b.runPlay(context.Background(), cfg, rctx, cfg.Ansible.Playbooks[0], true)
	if res.ExitCode != 0 || len(res.Hosts) != 1 || res.Hosts[0].Host != "host1" || res.Hosts[0].Changed != 1 {
		t.Fatalf("result = %+v", res)
	}

	log := readFake(t, bin, "docker.log")
	for _, want := range []string{
		"create -i --tmpfs /run/sf:rw,mode=0700",
		"ANSIBLE_SSH_COMMON_ARGS=-o UserKnownHostsFile=/work/known_hosts -o StrictHostKeyChecking=yes",
		"--entrypoint /bin/sh docker.io/hlhd/ansible:v1 -c",
		"'--private-key' '/run/sf/key'",
		"'--limit' 'web:db'",
		"'--check' '--diff'",
		"cp " + rctx.RepoRoot + " cid123:/work",
		"start -a -i cid123",
		"rm -f cid123",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("docker calls missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, " -v ") {
		t.Errorf("the workspace must be copied, never bind-mounted:\n%s", log)
	}
	if got := readFake(t, bin, "stdin"); got != "KEY\n" {
		t.Errorf("key over stdin = %q", got)
	}
}

// The native run holds the key in a private ssh-agent, never on disk, and cleans it up.
func TestRunPlay_DaemonlessRunner(t *testing.T) {
	bin := fakeBins(t, map[string]string{"ansible-playbook": fakeTool, "ssh-agent": fakeAgent, "ssh-add": fakeAdd})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_STDOUT", recapOutput)
	cfg, rctx := convergeFixture(t)
	b := &Backend{runtime: RuntimeNative, nativeBin: filepath.Join(bin, "ansible-playbook"), keyMaterial: []byte("KEY\n")}

	res := b.runPlay(context.Background(), cfg, rctx, cfg.Ansible.Playbooks[0], false)
	if res.ExitCode != 0 || len(res.Hosts) != 1 || res.Hosts[0].Host != "host1" {
		t.Fatalf("result = %+v", res)
	}

	args := readFake(t, bin, "ansible-playbook.log")
	if !strings.Contains(args, "-i inventory.yml site.yml --limit web:db -u deploy") {
		t.Errorf("argv = %q", args)
	}
	if strings.Contains(args, "--private-key") || strings.Contains(args, "--check") {
		t.Errorf("native apply passes no key file and no --check: %q", args)
	}
	if cwd := strings.TrimSpace(readFake(t, bin, "ansible-playbook.cwd")); cwd != rctx.RepoRoot {
		t.Errorf("cwd = %q, want the workspace %q", cwd, rctx.RepoRoot)
	}
	sock := strings.TrimSpace(readFake(t, bin, "agent.sock"))
	if got := strings.TrimSpace(readFake(t, bin, "ansible-playbook.sock")); got != sock {
		t.Errorf("ansible-playbook SSH_AUTH_SOCK = %q, want the agent's %q", got, sock)
	}
	if got := strings.TrimSpace(readFake(t, bin, "add.sock")); got != sock {
		t.Errorf("ssh-add loaded into %q, want the agent's %q", got, sock)
	}
	if got := readFake(t, bin, "added-key"); got != "KEY\n" {
		t.Errorf("key fed to ssh-add = %q", got)
	}
	if _, err := os.Stat(filepath.Dir(sock)); !os.IsNotExist(err) {
		t.Errorf("agent socket dir must be removed after the play: %v", err)
	}
}

// Both runtimes keep a failed play's exit; a setup failure is exit -1 with the reason.
func TestRunPlay_FailuresStayLoud(t *testing.T) {
	cfg, rctx := convergeFixture(t)

	bin := fakeBins(t, map[string]string{"docker": fakeDocker})
	t.Setenv("FAKE_STDOUT", "fatal: unreachable\n")
	t.Setenv("FAKE_EXIT", "4")
	b := &Backend{runtime: RuntimeImage, dockerBin: filepath.Join(bin, "docker")}
	if res := b.runPlay(context.Background(), cfg, rctx, cfg.Ansible.Playbooks[0], false); res.ExitCode != 4 || len(res.Hosts) != 0 {
		t.Errorf("docker runner failed play: %+v", res)
	}

	broken := fakeBins(t, map[string]string{"docker": "#!/bin/sh\necho 'no such image' >&2\nexit 1\n"})
	b = &Backend{runtime: RuntimeImage, dockerBin: filepath.Join(broken, "docker")}
	if res := b.runPlay(context.Background(), cfg, rctx, cfg.Ansible.Playbooks[0], false); res.ExitCode != -1 || !strings.Contains(res.Output, "container create failed: no such image") {
		t.Errorf("docker runner setup failure: %+v", res)
	}

	native := fakeBins(t, map[string]string{"ansible-playbook": fakeTool, "ssh-agent": fakeAgent, "ssh-add": fakeAdd})
	t.Setenv("PATH", native+string(os.PathListSeparator)+os.Getenv("PATH"))
	b = &Backend{runtime: RuntimeNative, nativeBin: filepath.Join(native, "ansible-playbook")}
	if res := b.runPlay(context.Background(), cfg, rctx, cfg.Ansible.Playbooks[0], false); res.ExitCode != 4 {
		t.Errorf("daemonless runner failed play: %+v", res)
	}
}

const lintReport = `[{"check_name": "name[missing]", "severity": "minor", "description": "All tasks should be named", "location": {"path": "site.yml", "lines": {"begin": 7}}}]`

// ansible-lint on both runtimes: exit 2 is a report, other exits are errors, no runtime refuses.
func TestLint_BothRunners(t *testing.T) {
	root := t.TempDir()
	plays := []string{"site.yml"}

	bin := fakeBins(t, map[string]string{"docker": fakeDocker})
	t.Setenv("FAKE_STDOUT", lintReport)
	t.Setenv("FAKE_STDERR", "WARNING [listing] noise\n")
	t.Setenv("FAKE_EXIT", "2")
	out, err := lint(context.Background(), substrate{daemon: true, dockerBin: filepath.Join(bin, "docker"), look: lookIn(bin)}, "docker.io/hlhd/ansible:v1", root, plays)
	if err != nil || string(out) != lintReport {
		t.Fatalf("docker runner: %q %v", out, err)
	}
	log := readFake(t, bin, "docker.log")
	if !strings.Contains(log, "cp "+root+" cid123:/work") || !strings.Contains(log, "'ansible-lint' '--format' 'json' '--nocolor' 'site.yml'") {
		t.Errorf("docker runner calls:\n%s", log)
	}
	if strings.Contains(log, " -v ") {
		t.Errorf("lint must copy the workspace, never bind-mount it (a remote daemon sees an empty mount):\n%s", log)
	}

	native := fakeBins(t, map[string]string{"ansible-lint": fakeTool})
	out, err = lint(context.Background(), substrate{look: lookIn(native)}, "docker.io/hlhd/ansible:v1", root, plays)
	if err != nil || string(out) != lintReport {
		t.Fatalf("daemonless runner: %q %v", out, err)
	}
	if cwd := strings.TrimSpace(readFake(t, native, "ansible-lint.cwd")); cwd != root {
		t.Errorf("native lint cwd = %q, want %q", cwd, root)
	}

	t.Setenv("FAKE_EXIT", "1")
	t.Setenv("FAKE_STDERR", "Couldn't parse task\n")
	if _, err := lint(context.Background(), substrate{look: lookIn(native)}, "img", root, plays); err == nil || !strings.Contains(err.Error(), "exited 1: Couldn't parse task") {
		t.Errorf("execution failure: %v", err)
	}

	if _, err := lint(context.Background(), substrate{look: lookIn(t.TempDir())}, "img", root, plays); err == nil || !strings.Contains(err.Error(), "nowhere to run") {
		t.Errorf("no runtime: %v", err)
	}
}
