package engines

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
)

// runnerKinds: a Docker host, and a daemonless runner with a remote buildkitd.
var runnerKinds = []struct {
	name string
	env  map[string]string
}{
	{"docker-daemon", map[string]string{
		"DOCKER_HOST": "unix:///var/run/docker.sock", "BUILDKIT_HOST": "",
	}},
	{"daemonless-buildkit", map[string]string{
		"DOCKER_HOST": "", "BUILDKIT_HOST": "tcp://buildkitd:1234",
	}},
}

func setRunnerKind(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// fakeDocker puts a logging `docker` on PATH whose `buildx build` exports exportDir, or fails with failMsg.
type fakeDocker struct {
	log, dockerfile string
}

func installFakeDocker(t *testing.T, exportDir, failMsg string) fakeDocker {
	t.Helper()
	bin := t.TempDir()
	fd := fakeDocker{log: filepath.Join(bin, "calls.log"), dockerfile: filepath.Join(bin, "Dockerfile.seen")}
	failFile := filepath.Join(bin, "fail.msg")
	if failMsg != "" {
		writeFile(t, failFile, failMsg+"\n", 0o644)
	}
	script := `#!/bin/sh
echo "$*" >> "` + fd.log + `"
[ "$1" = buildx ] && [ "$2" = build ] || exit 0
prev=""; dest=""; file=""
for a in "$@"; do
  case "$prev" in --output) dest="${a#type=local,dest=}";; --file) file="$a";; esac
  prev="$a"
done
cp "$file" "` + fd.dockerfile + `"
[ -f "$file.dockerignore" ] || { echo "no Dockerfile-specific .dockerignore beside $file" >&2; exit 1; }
if [ -f "` + failFile + `" ]; then cat "` + failFile + `" >&2; exit 1; fi
mkdir -p "$dest" && cp -a "` + exportDir + `"/. "$dest"/
`
	writeFile(t, filepath.Join(bin, "docker"), script, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fd
}

func (fd fakeDocker) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(fd.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// useBuilder registers a builder provider for the test, restored afterwards.
func useBuilder(t *testing.T, name string, err error) {
	t.Helper()
	build.RegisterBuildxBuilder(func(config.BuilderConfig) (string, error) { return name, err })
	t.Cleanup(func() { build.RegisterBuildxBuilder(nil) })
}

func writeFile(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// A step not on the job's image is one `buildx build --builder` whose export lands as the artifact, on either runner kind.
func TestContainerStep_BuildKit_BothRunnerKinds(t *testing.T) {
	for _, kind := range runnerKinds {
		t.Run(kind.name, func(t *testing.T) {
			setRunnerKind(t, kind.env)
			export := t.TempDir()
			writeFile(t, filepath.Join(export, "out", "f.txt"), "hi\n", 0o644)
			fd := installFakeDocker(t, export, "")
			useBuilder(t, "sf-builder", nil)

			repo := t.TempDir()
			t.Chdir(repo)
			step := build.UniversalStep{StepID: "s", Meta: ContainerMeta{
				Image: "docker.io/library/python:3.12-slim", Command: "mkdir -p out && echo hi > out/f.txt", Artifact: "out",
			}}
			res, err := (&containerEngine{name: EngineCommand, builder: "command"}).ExecuteStep(context.Background(), step)
			if err != nil {
				t.Fatalf("ExecuteStep: %v", err)
			}
			if len(res.Artifacts) != 1 || res.Artifacts[0].Type != "tree" {
				t.Fatalf("artifacts = %+v, want one tree", res.Artifacts)
			}
			if got, err := os.ReadFile(filepath.Join(repo, "out", "f.txt")); err != nil || string(got) != "hi\n" {
				t.Fatalf("artifact not landed in the workspace: %q %v", got, err)
			}
			if df, err := os.ReadFile(fd.dockerfile); err != nil || !strings.HasPrefix(string(df), "FROM docker.io/library/python:3.12-slim AS build\n") {
				t.Errorf("build definition = %q %v, want FROM the step's image", df, err)
			}
			calls := fd.calls(t)
			if len(calls) != 1 || !strings.HasPrefix(calls[0], "buildx build --builder sf-builder ") {
				t.Fatalf("docker calls = %q, want exactly one buildx build on sf-builder", calls)
			}
			if !strings.Contains(calls[0], "--output type=local,dest=") || !strings.HasSuffix(calls[0], " "+repo) {
				t.Errorf("build = %q, want a local export with the repo as context", calls[0])
			}
		})
	}
}

// With no usable builder the step fails, naming the image and the builder's reason.
func TestContainerStep_NoUsableBuilder(t *testing.T) {
	for _, kind := range runnerKinds {
		t.Run(kind.name, func(t *testing.T) {
			setRunnerKind(t, kind.env)
			fd := installFakeDocker(t, t.TempDir(), "")
			useBuilder(t, "", errors.New(`buildx builder "sf-builder": no backend available`))
			t.Chdir(t.TempDir())

			_, err := (&containerEngine{name: EngineNode, builder: "node"}).ExecuteStep(context.Background(),
				build.UniversalStep{Meta: ContainerMeta{Image: "node:22", Command: "true", Artifact: "dist"}})
			if err == nil || !strings.Contains(err.Error(), "node:22") || !strings.Contains(err.Error(), "no backend available") {
				t.Fatalf("err = %v, want the image and the builder's reason", err)
			}
			if calls := fd.calls(t); len(calls) != 0 {
				t.Errorf("docker was called anyway: %q", calls)
			}
		})
	}
}

// A command that produces nothing at the output path is reported by artifact path.
func TestContainerStep_BuildKit_MissingArtifact(t *testing.T) {
	for _, kind := range runnerKinds {
		t.Run(kind.name, func(t *testing.T) {
			setRunnerKind(t, kind.env)
			installFakeDocker(t, t.TempDir(), `failed to calculate checksum: "/workspace/out": not found`)
			useBuilder(t, "sf-builder", nil)
			t.Chdir(t.TempDir())

			_, err := (&containerEngine{name: EngineCommand, builder: "command"}).ExecuteStep(context.Background(),
				build.UniversalStep{Meta: ContainerMeta{Image: "busybox", Command: "true", Artifact: "out"}})
			if err == nil || !strings.Contains(err.Error(), `no artifact at "out"`) {
				t.Fatalf("err = %v, want it to name the missing artifact", err)
			}
		})
	}
}

// A step on the job's image runs in a scratch copy with no docker; only the artifact comes back.
func TestContainerStep_InJob_BothRunnerKinds(t *testing.T) {
	for _, kind := range runnerKinds {
		t.Run(kind.name, func(t *testing.T) {
			setRunnerKind(t, kind.env)
			fd := installFakeDocker(t, t.TempDir(), "")
			repo := t.TempDir()
			writeFile(t, filepath.Join(repo, "tool"), "#!/bin/sh\nmkdir -p \"$1\" && echo \"$GREETING\" > \"$1/f.txt\"\n", 0o755)
			t.Chdir(repo)

			step := build.UniversalStep{Meta: ContainerMeta{
				Image: "registry.example/ci:dev", InJob: true,
				Command:  "./tool docs && touch stray.txt",
				Env:      map[string]string{"GREETING": "hi"},
				Artifact: "docs",
			}}
			res, err := (&containerEngine{name: EngineCommand, builder: "command"}).ExecuteStep(context.Background(), step)
			if err != nil {
				t.Fatalf("ExecuteStep: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(repo, "docs", "f.txt")); err != nil || string(got) != "hi\n" {
				t.Fatalf("artifact = %q %v, want the command's output with declared env", got, err)
			}
			if len(res.Artifacts) != 1 || res.Artifacts[0].Path != filepath.Join(repo, "docs") {
				t.Errorf("artifacts = %+v", res.Artifacts)
			}
			if _, err := os.Stat(filepath.Join(repo, "stray.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a file the command wrote outside the artifact leaked into the workspace")
			}
			if calls := fd.calls(t); len(calls) != 0 {
				t.Errorf("in-job step called docker: %q", calls)
			}
		})
	}
}

// A failing in-job command reports its own output.
func TestContainerStep_InJob_FailureCarriesOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := (&containerEngine{name: EngineCommand, builder: "command"}).ExecuteStep(context.Background(),
		build.UniversalStep{Meta: ContainerMeta{Image: "ci", InJob: true, Command: "echo boom-reason >&2; exit 3", Artifact: "out"}})
	if err == nil || !strings.Contains(err.Error(), "boom-reason") {
		t.Fatalf("err = %v, want the command's output", err)
	}
}

// Only a step whose image is the job's runs in the job; with no job image, every step is a BuildKit build.
func TestContainerPlan_InJob(t *testing.T) {
	cases := []struct {
		name, image, job string
		want             bool
	}{
		{"image is the job's", "reg/ci:dev", "reg/ci:dev", true},
		{"another image", "python:3.12-slim", "reg/ci:dev", false},
		{"outside CI", "reg/ci:dev", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			steps, err := (&containerEngine{name: EngineCommand, builder: "command"}).Plan(context.Background(), build.BuildConfig{
				ID: "x", Kind: "command", Builder: "command", Image: c.image, JobImage: c.job,
				Command: "true", Output: "out", Platforms: []build.Target{{OS: "linux", Arch: "amd64"}},
				BuildxBuilder: config.BuilderConfig{Name: "custom"},
			})
			if err != nil {
				t.Fatal(err)
			}
			meta := steps[0].Meta.(ContainerMeta)
			if meta.InJob != c.want {
				t.Errorf("InJob = %v, want %v", meta.InJob, c.want)
			}
			if meta.BuildxBuilder.Name != "custom" {
				t.Errorf("BuildxBuilder not carried to the step: %+v", meta.BuildxBuilder)
			}
		})
	}
}

// The generated Dockerfile stages, quotes, mounts secrets and cache, and keeps only the artifact.
func TestCommandDockerfile(t *testing.T) {
	t.Setenv("KEYSTORE_PASS", "s3cret")
	meta := ContainerMeta{
		Image: "node:22", Command: `pnpm build && echo "done"`, WorkDir: "ui/app",
		Env:         map[string]string{"NODE_ENV": "production"},
		ForwardEnv:  []string{"KEYSTORE_PASS", "UNSET_SECRET"},
		CacheSubdir: []string{"node", "pnpm-store"}, CacheEnv: "PNPM_STORE_DIR",
	}
	got := commandDockerfile(meta, "ui/app/dist")
	for _, want := range []string{
		"FROM node:22 AS build\n",
		"COPY . /workspace\n",
		"WORKDIR /workspace/ui/app\n",
		"ARG NODE_ENV\n",
		"ENV PNPM_STORE_DIR=/pnpm-store\n",
		"--mount=type=cache,id=stagefreight-node-pnpm-store,target=/pnpm-store",
		"--mount=type=secret,id=KEYSTORE_PASS,env=KEYSTORE_PASS",
		`["sh","-c","pnpm build \u0026\u0026 echo \"done\""]`,
		"FROM scratch\nCOPY --from=build [\"/workspace/ui/app/dist\", \"/ui/app/dist\"]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "UNSET_SECRET") || strings.Contains(got, "s3cret") {
		t.Errorf("Dockerfile carries an unset secret or a secret value:\n%s", got)
	}

	args := strings.Join(commandBuildArgs("sf-builder", "/d/Dockerfile", "/d/out", "/repo", meta), " ")
	for _, want := range []string{
		"buildx build --builder sf-builder --progress=plain --file /d/Dockerfile --output type=local,dest=/d/out",
		"--build-arg NODE_ENV=production",
		"--secret id=KEYSTORE_PASS,env=KEYSTORE_PASS",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %s", want, args)
		}
	}
	if !strings.HasSuffix(args, " /repo") || strings.Contains(args, "s3cret") {
		t.Errorf("args = %s, want the repo as context and no secret value", args)
	}
}

// TestCopyPath keeps modes and symlinks and skips the named path.
func TestCopyPath(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(src, "bin", "tool"), "x", 0o755)
	writeFile(t, filepath.Join(src, "skip", "big"), "x", 0o644)
	if err := os.Symlink("bin/tool", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyPath(src, dst, filepath.Join(src, "skip")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "bin", "tool")); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("mode not kept: %v %v", fi, err)
	}
	if l, err := os.Readlink(filepath.Join(dst, "link")); err != nil || l != "bin/tool" {
		t.Errorf("symlink not kept: %q %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "skip")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("skipped path was copied")
	}
}

// An in-job command gets the base environment, its forwarded secrets and declared env —
// never the job's other credentials.
func TestInJobEnv_OnlyDeclaredInputs(t *testing.T) {
	t.Setenv("CI_JOB_TOKEN", "job-secret")
	t.Setenv("COSIGN_PASSWORD", "signing-secret")
	t.Setenv("KEYSTORE_PASS", "forwarded")
	env := strings.Join(inJobEnv(ContainerMeta{
		ForwardEnv: []string{"KEYSTORE_PASS"},
		Env:        map[string]string{"NODE_ENV": "production"},
	}), "\n")
	for _, want := range []string{"KEYSTORE_PASS=forwarded", "NODE_ENV=production", "PATH="} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q:\n%s", want, env)
		}
	}
	for _, leak := range []string{"CI_JOB_TOKEN", "COSIGN_PASSWORD"} {
		if strings.Contains(env, leak) {
			t.Errorf("env leaks %s:\n%s", leak, env)
		}
	}
}
