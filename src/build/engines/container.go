package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
				Image:      image,
				Command:    command,
				WorkDir:    cfg.From,
				Env:        cfg.Env,
				Artifact:   output,
				ForwardEnv: inf.ForwardEnv,
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

	// create (not run) so the container survives for the artifact cp-out; forward env
	// the same way — declared Env, plus named host vars (CI secrets) when actually set.
	createArgs := []string{"create", "-w", workdir}
	for k, v := range meta.Env {
		createArgs = append(createArgs, "-e", k+"="+v)
	}
	for _, name := range meta.ForwardEnv {
		if v, ok := os.LookupEnv(name); ok {
			createArgs = append(createArgs, "-e", name+"="+v)
		}
	}
	createArgs = append(createArgs, meta.Image, "sh", "-c", meta.Command)

	idOut, err := exec.CommandContext(ctx, "docker", createArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("container engine: docker create (%s): %w", meta.Image, err)
	}
	containerID := strings.TrimSpace(string(idOut))
	defer exec.Command("docker", "rm", "-f", containerID).Run()

	// Stage the repo into the container at the workspace root. The trailing "/." copies
	// the source directory's CONTENTS into /workspace whether or not it pre-exists —
	// and `-w` pre-creates it, which would otherwise nest the repo under /workspace/<base>.
	if out, cerr := exec.CommandContext(ctx, "docker", "cp", rootDir+"/.", containerID+":"+containerRoot).CombinedOutput(); cerr != nil {
		return nil, fmt.Errorf("container engine: staging workspace into container: %w\n%s", cerr, string(out))
	}

	// Run the build command.
	if out, rerr := exec.CommandContext(ctx, "docker", "start", "-a", containerID).CombinedOutput(); rerr != nil {
		return nil, fmt.Errorf("container build failed (%s): %w\n%s", meta.Image, rerr, string(out))
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
