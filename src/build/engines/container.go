package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// The containerized-build engine runs a build COMMAND inside a declared container
// IMAGE (repo mounted) and captures the produced artifact — a file OR a directory
// tree — for languages StageFreight doesn't compile natively. engineNameFor(<b>)
// resolves to "binary-<b>", so each builder routes here and rides the binary
// contributor's orchestration + archiving. One engine, one substrate; each language
// is a small convention (inferBuild) on top. node was first; elixir rides the same
// rails at a fraction of the cost — which is the whole point.
const (
	EngineNode   = "binary-node"
	EngineElixir = "binary-elixir"
	EngineDotnet = "binary-dotnet"
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

// cacheBindMountable reports whether the build cache can be bind-mounted into the
// container instead of copied — true when the daemon shares our filesystem (a local
// unix socket), so a `-v` mount resolves to the same path we wrote. Under DinD the
// daemon is a separate host, so the default is the docker-cp bridge; an operator who
// has mounted the SF cache into the DinD service at the same path can force the
// zero-copy mount with SF_CONTAINER_CACHE_BIND=1.
func cacheBindMountable() bool {
	if os.Getenv("SF_CONTAINER_CACHE_BIND") == "1" {
		return true
	}
	h := strings.TrimSpace(os.Getenv("DOCKER_HOST"))
	return h == "" || strings.HasPrefix(h, "unix://")
}

func (e *containerEngine) ExecuteStep(ctx context.Context, step build.UniversalStep) (*build.UniversalStepResult, error) {
	meta, ok := step.Meta.(ContainerMeta)
	if !ok {
		return nil, fmt.Errorf("container engine: expected ContainerMeta, got %T", step.Meta)
	}
	start := time.Now()

	rootDir, _ := os.Getwd()

	// The container engine is daemon-agnostic. It does NOT bind-mount the workspace:
	// a `-v` mount resolves against the *daemon's* filesystem, which under DinD is a
	// separate host from the job checkout, so the produced artifact never lands where
	// the pipeline looks. Instead we `docker cp` the repo INTO a created container,
	// run the command, then `docker cp` the produced artifact back OUT onto the host.
	// Both stream over the daemon API, so this works with a local OR a remote/DinD
	// daemon (Crucible's buildkit-cert plumbing is still not needed here).
	const containerRoot = "/workspace"
	workdir := containerRoot
	if meta.WorkDir != "" {
		workdir = path.Join(containerRoot, meta.WorkDir)
	}

	// Persistent package-manager cache (best-effort). Reuse a content-addressed store
	// across CI runs instead of re-downloading every dependency. It lives under the SF
	// cache root beside the Go/Rust caches (e.g. /stagefreight/cache/node/pnpm-store) and
	// is bridged into the build container: bind-mounted when the daemon shares our
	// filesystem (zero-copy), else docker-cp'd in and back out under DinD. ANY failure
	// degrades to a cold build — never fatal. No-op when no persistent cache mount exists
	// (ContainerCacheDir returns ""), e.g. local dev.
	var cacheHostDir, cacheContainerDir string
	bindCache := false
	if len(meta.CacheSubdir) > 0 && meta.CacheEnv != "" {
		if d := toolchain.ContainerCacheDir(meta.CacheSubdir...); d != "" {
			cacheHostDir = d
			cacheContainerDir = "/" + meta.CacheSubdir[len(meta.CacheSubdir)-1]
			bindCache = cacheBindMountable()
		}
	}

	// create (not run) so the container survives for the artifact cp-out; forward env
	// the same way — declared Env, plus named host vars (CI secrets) when actually set.
	createArgs := []string{"create", "-w", workdir}
	if cacheContainerDir != "" {
		createArgs = append(createArgs, "-e", meta.CacheEnv+"="+cacheContainerDir)
		if bindCache {
			createArgs = append(createArgs, "-v", cacheHostDir+":"+cacheContainerDir)
		}
	}
	for k, v := range meta.Env {
		createArgs = append(createArgs, "-e", k+"="+v)
	}
	for _, name := range meta.ForwardEnv {
		if v, ok := os.LookupEnv(name); ok {
			createArgs = append(createArgs, "-e", name+"="+v)
		}
	}
	createArgs = append(createArgs, meta.Image, "sh", "-c", meta.Command)

	// A cold pull of a large builder image (electron/wine images are ~3–4 GiB) on a
	// disk-tight runner is the usual cause of a create failure. If the image isn't
	// already cached AND free disk is low, warn BEFORE the create so the eventual
	// failure isn't a surprise — the runner preflight green-lights on free disk alone
	// without knowing the image's size.
	cached := imageCached(ctx, meta.Image)
	if !cached {
		if free := freeDiskGiB(rootDir); free >= 0 && free < 5 {
			fmt.Fprintf(os.Stderr, "  warning: build image %s is not cached and only %.1f GiB is free on the runner — a large image pull may exhaust disk; pre-cache the image or pin this step to a larger runner\n", meta.Image, free)
		}
	}

	idOut, err := exec.CommandContext(ctx, "docker", createArgs...).Output()
	if err != nil {
		// Surface the docker stderr (Cmd.Output captures it into ExitError.Stderr), so a
		// bare "exit status 1" carries the real reason (e.g. "no space left on device").
		hint := ""
		if !cached {
			hint = fmt.Sprintf("\nhint: %s was not cached, so this is a pull failure — commonly the runner is out of disk (free space / pre-cache the image / use a larger runner), or a registry auth/network problem", meta.Image)
		}
		return nil, fmt.Errorf("container engine: docker create (%s): %w%s%s", meta.Image, err, stderrTail(err), hint)
	}
	containerID := strings.TrimSpace(string(idOut))
	defer exec.Command("docker", "rm", "-f", containerID).Run()

	// Stage the repo into the container at the workspace root. The trailing "/." copies
	// the source directory's CONTENTS into /workspace whether or not it pre-exists —
	// and `-w` pre-creates it, which would otherwise nest the repo under /workspace/<base>.
	if out, cerr := exec.CommandContext(ctx, "docker", "cp", rootDir+"/.", containerID+":"+containerRoot).CombinedOutput(); cerr != nil {
		return nil, fmt.Errorf("container engine: staging workspace into container: %w\n%s", cerr, string(out))
	}

	// Seed the dependency cache into the container (DinD/cp path only — a bind-mount is
	// already live). Best-effort: `docker cp <dir> :/` lands it at /<basename> =
	// cacheContainerDir; a miss just yields a cold install the cp-out below still persists.
	if cacheHostDir != "" && !bindCache {
		_ = exec.CommandContext(ctx, "docker", "cp", cacheHostDir, containerID+":/").Run()
	}

	// Run the build command.
	if out, rerr := exec.CommandContext(ctx, "docker", "start", "-a", containerID).CombinedOutput(); rerr != nil {
		return nil, fmt.Errorf("container build failed (%s): %w\n%s", meta.Image, rerr, string(out))
	}

	// Persist the (possibly grown) dependency cache back to the host store — DinD/cp path
	// only; a bind-mount already wrote through. Best-effort and content-addressed, so a
	// partial copy only costs a future re-download, never correctness.
	if cacheHostDir != "" && !bindCache {
		_ = exec.CommandContext(ctx, "docker", "cp", containerID+":"+cacheContainerDir, filepath.Dir(cacheHostDir)).Run()
	}

	// Copy the produced artifact back onto the host at its in-repo path, so the rest
	// of the pipeline (archive, transport) reads it exactly as before. The output is a
	// glob (an installer: release/*.exe), a single file, or a directory tree (dist/).
	// docker cp can't glob, so for a glob artifact we extract its fixed leading
	// directory and let the host-side glob below resolve the pattern within it.
	extractRel := meta.Artifact
	if strings.ContainsAny(meta.Artifact, "*?[") {
		extractRel = globBaseDir(meta.Artifact)
	}
	hostDest := filepath.Join(rootDir, extractRel)
	if err := os.MkdirAll(filepath.Dir(hostDest), 0o755); err != nil {
		return nil, fmt.Errorf("container engine: preparing artifact dir: %w", err)
	}
	_ = os.RemoveAll(hostDest) // deterministic landing: DEST must not pre-exist
	if out, xerr := exec.CommandContext(ctx, "docker", "cp", containerID+":"+path.Join(containerRoot, extractRel), hostDest).CombinedOutput(); xerr != nil {
		return nil, fmt.Errorf("container engine: no artifact at %q after the build: %w\n%s", meta.Artifact, xerr, string(out))
	}

	// Resolve the produced artifact(s) on the host now. A tree is one artifact; the
	// archive walks it.
	artifactPath := meta.Artifact
	if !filepath.IsAbs(artifactPath) {
		artifactPath = filepath.Join(rootDir, artifactPath)
	}
	var matches []string
	if strings.ContainsAny(meta.Artifact, "*?[") {
		m, gerr := filepath.Glob(artifactPath)
		if gerr != nil {
			return nil, fmt.Errorf("container engine: bad artifact glob %q: %w", meta.Artifact, gerr)
		}
		matches = m
	} else if _, serr := os.Stat(artifactPath); serr == nil {
		matches = []string{artifactPath}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("container engine: no artifact at %q after the build", meta.Artifact)
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

	// binary_name is what the binary-archive target names the file inside the
	// archive; use the produced artifact's basename (e.g. the .exe filename).
	binaryName := filepath.Base(artifacts[0].Path)

	return &build.UniversalStepResult{
		Artifacts: artifacts,
		Metadata:  map[string]string{"image": meta.Image, "binary_name": binaryName},
		Metrics:   build.StepMetrics{Duration: time.Since(start)},
	}, nil
}

// globBaseDir returns the longest leading path of a glob pattern that contains no
// wildcard, so the concrete subtree can be docker-cp'd out before host-side globbing
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

// stderrTail returns the captured stderr of a failed exec, newline-prefixed, or "".
// Cmd.Output() populates ExitError.Stderr when Cmd.Stderr is nil, so this recovers the
// real diagnostic (e.g. "no space left on device") that would otherwise be lost behind
// a bare "exit status 1".
func stderrTail(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if s := strings.TrimSpace(string(ee.Stderr)); s != "" {
			return "\n" + s
		}
	}
	return ""
}

// imageCached reports whether the image already exists on the target daemon, so no
// pull happens on create (a cold pull is the risky, disk-hungry path).
func imageCached(ctx context.Context, image string) bool {
	return exec.CommandContext(ctx, "docker", "image", "inspect", image).Run() == nil
}

// freeDiskGiB returns the free space (GiB) on dir's filesystem, or -1 if unknown. Uses
// the same syscall.Statfs the runner preflight uses.
func freeDiskGiB(dir string) float64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return -1
	}
	return float64(st.Bavail) * float64(st.Bsize) / (1 << 30)
}
