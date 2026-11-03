package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// The containerized-build engine runs a build COMMAND inside a declared container
// IMAGE (the repo staged in) and captures the produced artifact — a file OR a
// directory tree — for languages StageFreight doesn't compile natively. engineNameFor(<b>)
// resolves to "binary-<b>", so each builder routes here and rides the binary
// contributor's orchestration + archiving. One engine, one substrate; each language
// is a small convention (inferBuild) on top. node was first; elixir rides the same
// rails at a fraction of the cost — which is the whole point.
const (
	EngineNode    = "binary-node"
	EngineElixir  = "binary-elixir"
	EngineDotnet  = "binary-dotnet"
	EngineC       = "binary-c"
	EnginePython  = "binary-python"
	EngineJVM     = "binary-jvm"
	EngineAndroid = "binary-android"
	EngineCommand = "binary-command"
)

func init() {
	build.RegisterV2(EngineNode, func() build.EngineV2 { return &containerEngine{name: EngineNode, builder: "node"} })
	build.RegisterV2(EngineCommand, func() build.EngineV2 { return &containerEngine{name: EngineCommand, builder: "command"} })
	build.RegisterV2(EngineElixir, func() build.EngineV2 { return &containerEngine{name: EngineElixir, builder: "elixir"} })
	build.RegisterV2(EngineDotnet, func() build.EngineV2 { return &containerEngine{name: EngineDotnet, builder: "dotnet"} })
	build.RegisterV2(EngineC, func() build.EngineV2 { return &containerEngine{name: EngineC, builder: "c"} })
	build.RegisterV2(EnginePython, func() build.EngineV2 { return &containerEngine{name: EnginePython, builder: "python"} })
	build.RegisterV2(EngineJVM, func() build.EngineV2 { return &containerEngine{name: EngineJVM, builder: "jvm"} })
	build.RegisterV2(EngineAndroid, func() build.EngineV2 { return &containerEngine{name: EngineAndroid, builder: "android"} })
}

// containerEngine is shared across all containerized builders; builder selects the
// convention (inferBuild), name is the dispatch key it was registered under.
type containerEngine struct {
	name    string
	builder string
}

func (e *containerEngine) Name() string { return e.name }

func (e *containerEngine) Capabilities() build.Capabilities {
	return build.Capabilities{
		// The image cross-builds (e.g. a Windows .exe via wine on Linux).
		SupportsCrossCompile: true,
		SupportsCrucible:     false,
		ProducesArchives:     false, // archives remain a target concern
		ProducesOCI:          false,
	}
}

func (e *containerEngine) Detect(ctx context.Context, rootDir string) (*build.Detection, error) {
	return build.DetectRepo(rootDir)
}

func (e *containerEngine) Plan(ctx context.Context, cfg build.BuildConfig) ([]build.UniversalStep, error) {
	// kind: command runs at the repo root by default (from is optional); the language
	// builders need a package directory.
	if cfg.From == "" && e.builder != "command" {
		return nil, fmt.Errorf("container engine: from is required (the package directory, e.g. ui/desktop)")
	}
	rootDir, _ := os.Getwd()

	var steps []build.UniversalStep
	for _, tgt := range cfg.Platforms {
		// The engine owns the build: the per-builder convention (inferBuild) fills
		// image/command/output; explicit config overlays on top — the escape hatch,
		// not the norm. Inference is per-target since the image/output vary by OS.
		inf := inferBuild(e.builder, rootDir, cfg.From, tgt.OS, tgt.Arch)
		image := firstNonEmpty(cfg.Image, inf.Image)
		command := resolveTemplateVars(firstNonEmpty(cfg.Command, inf.Command), cfg)
		output := resolveTemplateVars(firstNonEmpty(cfg.Output, inf.Output), cfg)

		// Guard against an unresolved output (e.g. a raw Makefile, where the artifact
		// location is unknowable) — never fall through to capturing the repo root.
		if output == "" {
			return nil, fmt.Errorf("container engine: could not infer the artifact location — set output: to the produced file or directory")
		}

		steps = append(steps, build.UniversalStep{
			BuildID: cfg.ID,
			StepID:  build.StepIDForTarget(cfg.ID, tgt),
			Engine:  e.name,
			Target:  tgt,
			Outputs: []build.ArtifactRef{{Path: output, Type: "binary"}},
			Meta: ContainerMeta{
				Image:       image,
				Command:     command,
				WorkDir:     cfg.From,
				Env:         cfg.Env,
				Artifact:    output,
				ForwardEnv:  inf.ForwardEnv,
				CacheSubdir: inf.CacheSubdir,
				CacheEnv:    inf.CacheEnv,

				InJob:         cfg.JobImage != "" && image == cfg.JobImage,
				BuildxBuilder: cfg.BuildxBuilder,
			},
		})
	}
	return steps, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (e *containerEngine) ExecuteStep(ctx context.Context, step build.UniversalStep) (*build.UniversalStepResult, error) {
	meta, ok := step.Meta.(ContainerMeta)
	if !ok {
		return nil, fmt.Errorf("container engine: expected ContainerMeta, got %T", step.Meta)
	}
	start := time.Now()

	rootDir, _ := os.Getwd()

	// Either way only the artifact comes back; nothing else the command writes reaches the workspace.
	run := runOnBuildKit
	if meta.InJob {
		run = runInJob
	}
	if err := run(ctx, rootDir, meta); err != nil {
		return nil, err
	}

	artifacts, err := collectArtifacts(rootDir, meta.Artifact)
	if err != nil {
		return nil, err
	}

	// binary_name is what the binary-archive target names the file inside the
	// archive; use the produced artifact's basename (e.g. the .exe filename).
	binaryName := filepath.Base(artifacts[0].Path)

	return &build.UniversalStepResult{
		Artifacts: artifacts,
		Metadata:  map[string]string{"image": meta.Image, "binary_name": binaryName},
		Metrics:   build.StepMetrics{Duration: time.Since(start)},
	}, nil
}

// runInJob runs the command in this job (its image is the job's image) in a scratch
// copy of the workspace, then lands the artifact.
func runInJob(ctx context.Context, rootDir string, meta ContainerMeta) error {
	scratch, err := os.MkdirTemp("", "stagefreight-command-")
	if err != nil {
		return fmt.Errorf("container engine: scratch workspace: %w", err)
	}
	defer os.RemoveAll(scratch)
	if err := copyPath(rootDir, scratch, scratch); err != nil {
		return fmt.Errorf("container engine: staging workspace: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", meta.Command)
	cmd.Dir = scratch
	if meta.WorkDir != "" {
		cmd.Dir = filepath.Join(scratch, meta.WorkDir)
	}
	cmd.Env = inJobEnv(meta)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("command build failed (in this job, image %s): %w\n%s", meta.Image, err, string(out))
	}
	return landArtifact(scratch, rootDir, meta.Artifact)
}

// runOnBuildKit runs the step as a BuildKit build on the run's buildx builder and
// exports only the artifact (type=local).
func runOnBuildKit(ctx context.Context, rootDir string, meta ContainerMeta) error {
	builder, err := build.EnsureBuildxBuilder(meta.BuildxBuilder)
	if err != nil {
		return fmt.Errorf("container engine: %s runs on a BuildKit builder, and none is usable: %w", meta.Image, err)
	}

	defDir, err := os.MkdirTemp("", "stagefreight-command-")
	if err != nil {
		return fmt.Errorf("container engine: build definition dir: %w", err)
	}
	defer os.RemoveAll(defDir)

	extractRel := artifactExtractRel(meta.Artifact)
	dockerfile := filepath.Join(defDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte(commandDockerfile(meta, extractRel)), 0o644); err != nil {
		return fmt.Errorf("container engine: writing build definition: %w", err)
	}
	// An empty Dockerfile-specific ignore file overrides the repo's .dockerignore: the command sees the whole repo.
	if err := os.WriteFile(dockerfile+".dockerignore", nil, 0o644); err != nil {
		return fmt.Errorf("container engine: writing build definition: %w", err)
	}
	outDir := filepath.Join(defDir, "out")

	out, err := exec.CommandContext(ctx, "docker", commandBuildArgs(builder, dockerfile, outDir, rootDir, meta)...).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), fmt.Sprintf("%q: not found", path.Join(containerRoot, extractRel))) {
			return fmt.Errorf("container engine: no artifact at %q after the build\n%s", meta.Artifact, string(out))
		}
		return fmt.Errorf("container build failed (%s on builder %s): %w\n%s", meta.Image, builder, err, string(out))
	}
	return landArtifact(outDir, rootDir, meta.Artifact)
}

// containerRoot is where the repo is staged inside the step's image.
const containerRoot = "/workspace"

// commandDockerfile is the BuildKit definition of a containerized step; the final
// scratch stage holds only the artifact. CI secrets are secret mounts, never layers.
func commandDockerfile(meta ContainerMeta, extractRel string) string {
	workdir := containerRoot
	if meta.WorkDir != "" {
		workdir = path.Join(containerRoot, meta.WorkDir)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s AS build\n", meta.Image)
	fmt.Fprintf(&b, "COPY . %s\n", containerRoot)
	fmt.Fprintf(&b, "WORKDIR %s\n", workdir)
	for _, k := range sortedKeys(meta.Env) {
		fmt.Fprintf(&b, "ARG %s\n", k)
	}
	var mounts []string
	if len(meta.CacheSubdir) > 0 && meta.CacheEnv != "" {
		target := "/" + meta.CacheSubdir[len(meta.CacheSubdir)-1]
		fmt.Fprintf(&b, "ENV %s=%s\n", meta.CacheEnv, target)
		mounts = append(mounts, fmt.Sprintf("--mount=type=cache,id=stagefreight-%s,target=%s",
			strings.Join(meta.CacheSubdir, "-"), target))
	}
	for _, name := range forwardedEnv(meta.ForwardEnv) {
		mounts = append(mounts, fmt.Sprintf("--mount=type=secret,id=%s,env=%s", name, name))
	}
	cmd, _ := json.Marshal([]string{"sh", "-c", meta.Command})
	b.WriteString("RUN ")
	for _, m := range mounts {
		b.WriteString(m + " ")
	}
	b.Write(cmd)
	b.WriteString("\n")
	src, _ := json.Marshal(path.Join(containerRoot, extractRel))
	dst, _ := json.Marshal(path.Join("/", extractRel))
	fmt.Fprintf(&b, "FROM scratch\nCOPY --from=build [%s, %s]\n", src, dst)
	return b.String()
}

// commandBuildArgs is the buildx invocation for a containerized step; it names the builder explicitly.
func commandBuildArgs(builder, dockerfile, outDir, rootDir string, meta ContainerMeta) []string {
	args := []string{"buildx", "build", "--builder", builder, "--progress=plain",
		"--file", dockerfile, "--output", "type=local,dest=" + outDir}
	for _, k := range sortedKeys(meta.Env) {
		args = append(args, "--build-arg", k+"="+meta.Env[k])
	}
	for _, name := range forwardedEnv(meta.ForwardEnv) {
		args = append(args, "--secret", "id="+name+",env="+name)
	}
	return append(args, rootDir)
}

// forwardedEnv is the subset of the named host vars that are actually set, in order.
// inJobBaseEnv is what an in-job command inherits from the job: what a shell and its
// tools need to run, never the job's credentials.
var inJobBaseEnv = []string{
	"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

// inJobEnv is an in-job command's environment: the base, the step's forwarded secrets,
// its declared env and its cache dir — the same inputs the BuildKit path gets.
func inJobEnv(meta ContainerMeta) []string {
	var env []string
	for _, n := range inJobBaseEnv {
		if v, ok := os.LookupEnv(n); ok {
			env = append(env, n+"="+v)
		}
	}
	for _, n := range forwardedEnv(meta.ForwardEnv) {
		env = append(env, n+"="+os.Getenv(n))
	}
	for _, k := range sortedKeys(meta.Env) {
		env = append(env, k+"="+meta.Env[k])
	}
	if len(meta.CacheSubdir) > 0 && meta.CacheEnv != "" {
		if d := toolchain.ContainerCacheDir(meta.CacheSubdir...); d != "" {
			env = append(env, meta.CacheEnv+"="+d)
		}
	}
	return env
}

func forwardedEnv(names []string) []string {
	var set []string
	for _, n := range names {
		if _, ok := os.LookupEnv(n); ok {
			set = append(set, n)
		}
	}
	return set
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// artifactExtractRel is the path to carry out for an artifact: itself, or a glob's fixed leading directory.
func artifactExtractRel(artifact string) string {
	if strings.ContainsAny(artifact, "*?[") {
		return globBaseDir(artifact)
	}
	return artifact
}

// landArtifact copies the artifact from srcRoot onto the workspace at its in-repo path.
func landArtifact(srcRoot, rootDir, artifact string) error {
	rel := artifactExtractRel(artifact)
	src := filepath.Join(srcRoot, rel)
	if _, err := os.Lstat(src); err != nil {
		return fmt.Errorf("container engine: no artifact at %q after the build", artifact)
	}
	hostDest := filepath.Join(rootDir, rel)
	if err := os.MkdirAll(filepath.Dir(hostDest), 0o755); err != nil {
		return fmt.Errorf("container engine: preparing artifact dir: %w", err)
	}
	if err := os.RemoveAll(hostDest); err != nil { // deterministic landing
		return fmt.Errorf("container engine: clearing %s: %w", hostDest, err)
	}
	if err := copyPath(src, hostDest, ""); err != nil {
		return fmt.Errorf("container engine: landing artifact %q: %w", artifact, err)
	}
	return nil
}

// collectArtifacts resolves the landed artifact(s): a glob, a file, or one directory tree.
func collectArtifacts(rootDir, artifact string) ([]build.ProducedArtifact, error) {
	artifactPath := artifact
	if !filepath.IsAbs(artifactPath) {
		artifactPath = filepath.Join(rootDir, artifactPath)
	}
	var matches []string
	if strings.ContainsAny(artifact, "*?[") {
		m, gerr := filepath.Glob(artifactPath)
		if gerr != nil {
			return nil, fmt.Errorf("container engine: bad artifact glob %q: %w", artifact, gerr)
		}
		matches = m
	} else if _, serr := os.Stat(artifactPath); serr == nil {
		matches = []string{artifactPath}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("container engine: no artifact at %q after the build", artifact)
	}
	sort.Strings(matches)

	artifacts := make([]build.ProducedArtifact, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return nil, fmt.Errorf("container engine: stat artifact %s: %w", m, err)
		}
		if info.IsDir() {
			// A tree artifact: size is the sum of files; integrity is carried by the
			// archive that wraps it (a directory has no single content hash).
			size, derr := dirSize(m)
			if derr != nil {
				return nil, fmt.Errorf("container engine: sizing %s: %w", m, derr)
			}
			artifacts = append(artifacts, build.ProducedArtifact{Path: m, Type: "tree", Size: size})
			continue
		}
		sum, herr := fileSHA256(m)
		if herr != nil {
			return nil, fmt.Errorf("container engine: hashing %s: %w", m, herr)
		}
		artifacts = append(artifacts, build.ProducedArtifact{Path: m, Type: "binary", Size: info.Size(), SHA256: sum})
	}
	return artifacts, nil
}

// copyPath copies a file, symlink or tree, keeping modes and links. Never a hardlink:
// the command may rewrite files in place. skip is a path not to descend into.
func copyPath(src, dst, skip string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if skip != "" && p == skip {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch mode := info.Mode(); {
		case mode&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case mode.IsDir():
			return os.MkdirAll(target, mode.Perm()|0o700)
		case mode.IsRegular():
			return copyFileMode(p, target, mode.Perm())
		default:
			return nil // sockets, devices, pipes: not repo content
		}
	})
}

func copyFileMode(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// globBaseDir returns the longest leading path of a glob pattern that contains no
// wildcard, so the concrete subtree can be carried out before host-side globbing
// resolves the pattern within it. "release/*.exe" -> "release"; "*.exe" -> ".".
func globBaseDir(pattern string) string {
	var base []string
	for _, p := range strings.Split(pattern, "/") {
		if strings.ContainsAny(p, "*?[") {
			break
		}
		base = append(base, p)
	}
	if len(base) == 0 {
		return "."
	}
	return strings.Join(base, "/")
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
