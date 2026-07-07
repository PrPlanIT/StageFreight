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
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
)

// EngineNode is the dispatch key for containerized builds. engineNameFor("node")
// resolves to "binary-node", so a `builder: node` build routes here and rides the
// binary contributor's orchestration + archiving. Unlike binary-go/binary-rust,
// which compile natively, this engine runs a build COMMAND inside a declared
// container IMAGE (repo mounted) and collects the produced artifact — the general
// "run this build in this image, capture the output" primitive. Its first user is
// Electron packaging via electronuserland/builder:wine, but it's build-agnostic.
const EngineNode = "binary-node"

func init() {
	build.RegisterV2(EngineNode, func() build.EngineV2 { return &nodeEngine{} })
}

type nodeEngine struct{}

func (e *nodeEngine) Name() string { return EngineNode }

func (e *nodeEngine) Capabilities() build.Capabilities {
	return build.Capabilities{
		// The image cross-builds (e.g. a Windows .exe via wine on Linux).
		SupportsCrossCompile: true,
		SupportsCrucible:     false,
		ProducesArchives:     false, // archives remain a target concern
		ProducesOCI:          false,
	}
}

func (e *nodeEngine) Detect(ctx context.Context, rootDir string) (*build.Detection, error) {
	return build.DetectRepo(rootDir)
}

func (e *nodeEngine) Plan(ctx context.Context, cfg build.BuildConfig) ([]build.UniversalStep, error) {
	if cfg.From == "" {
		return nil, fmt.Errorf("container engine: from is required (the package directory, e.g. ui/desktop)")
	}
	rootDir, _ := os.Getwd()

	var steps []build.UniversalStep
	for _, tgt := range cfg.Platforms {
		// The engine owns the build: convention (install → build → pack) fills
		// image/command/output; explicit config overlays on top — the escape hatch,
		// not the norm. Inference is per-target since the image/output vary by OS.
		inf := inferNodeBuild(rootDir, cfg.From, tgt.OS)
		image := firstNonEmpty(cfg.Image, inf.Image)
		command := resolveTemplateVars(firstNonEmpty(cfg.Command, inf.Command), cfg)
		output := resolveTemplateVars(firstNonEmpty(cfg.Output, inf.Output), cfg)

		steps = append(steps, build.UniversalStep{
			BuildID: cfg.ID,
			StepID:  build.StepIDForTarget(cfg.ID, tgt),
			Engine:  EngineNode,
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

func (e *nodeEngine) ExecuteStep(ctx context.Context, step build.UniversalStep) (*build.UniversalStepResult, error) {
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

	// Collect the produced artifact(s) by glob, relative to the repo root — they
	// were written into the mounted volume, so they're on the host now.
	pattern := meta.Artifact
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(rootDir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("container engine: bad artifact glob %q: %w", meta.Artifact, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("container engine: no artifact matched %q after the build", meta.Artifact)
	}
	sort.Strings(matches)

	artifacts := make([]build.ProducedArtifact, 0, len(matches))
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			return nil, fmt.Errorf("container engine: stat artifact %s: %w", m, err)
		}
		sum, err := fileSHA256(m)
		if err != nil {
			return nil, fmt.Errorf("container engine: hashing %s: %w", m, err)
		}
		artifacts = append(artifacts, build.ProducedArtifact{
			Path:   m,
			Type:   "binary",
			Size:   info.Size(),
			SHA256: sum,
		})
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
