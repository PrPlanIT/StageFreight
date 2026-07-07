package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	EngineC      = "binary-c"
	EnginePython = "binary-python"
	EngineJVM    = "binary-jvm"
)

func init() {
	build.RegisterV2(EngineNode, func() build.EngineV2 { return &containerEngine{name: EngineNode, builder: "node"} })
	build.RegisterV2(EngineElixir, func() build.EngineV2 { return &containerEngine{name: EngineElixir, builder: "elixir"} })
	build.RegisterV2(EngineDotnet, func() build.EngineV2 { return &containerEngine{name: EngineDotnet, builder: "dotnet"} })
	build.RegisterV2(EngineC, func() build.EngineV2 { return &containerEngine{name: EngineC, builder: "c"} })
	build.RegisterV2(EnginePython, func() build.EngineV2 { return &containerEngine{name: EnginePython, builder: "python"} })
	build.RegisterV2(EngineJVM, func() build.EngineV2 { return &containerEngine{name: EngineJVM, builder: "jvm"} })
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
	if cfg.From == "" {
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
				Image:    image,
				Command:  command,
				WorkDir:  cfg.From,
				Env:      cfg.Env,
				Artifact: output,
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
	workdir := rootDir
	if meta.WorkDir != "" {
		workdir = filepath.Join(rootDir, meta.WorkDir)
	}

	// docker run --rm, repo mounted at its own absolute path, cwd = workdir,
	// forwarded env, build command via `sh -c`. This is the general containerized
	// build primitive — electron's wine case needs none of Crucible's DinD /
	// buildkit-cert plumbing, so it stays deliberately minimal.
	args := []string{"run", "--rm", "-v", rootDir + ":" + rootDir, "-w", workdir}
	for k, v := range meta.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, meta.Image, "sh", "-c", meta.Command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("container build failed (%s): %w\n%s", meta.Image, err, string(output))
	}

	// Collect the produced artifact(s) — written into the mounted volume, so on the
	// host now. The output is a glob (an installer: release/*.exe), a single file,
	// or a directory (a built tree: dist/). A tree is one artifact; the archive
	// walks it.
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
