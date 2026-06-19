package engines

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// EngineGo is the engine name for the Go binary builder — the dispatch key the
// binary contributor resolves for `builder: go`. Sibling language engines register
// as "binary-<builder>" (e.g. binary-rust), so the contributor stays a generic
// pipeline that dispatches per builder, with Go as the first implementation.
const EngineGo = "binary-go"

func init() {
	build.RegisterV2(EngineGo, func() build.EngineV2 { return &binaryEngine{} })
}

// binaryEngine compiles Go binaries. Plan + ExecuteStep only.
// All orchestration (ordering, concurrency, logging, artifact recording,
// checksums, publish manifest) lives in core.
type binaryEngine struct{}

func (e *binaryEngine) Name() string { return EngineGo }

func (e *binaryEngine) Capabilities() build.Capabilities {
	return build.Capabilities{
		SupportsCrossCompile: true,
		SupportsCrucible:     true,
		ProducesArchives:     false, // archives are a target concern, not engine
		ProducesOCI:          false,
	}
}

func (e *binaryEngine) Detect(ctx context.Context, rootDir string) (*build.Detection, error) {
	det, err := build.DetectRepo(rootDir)
	if err != nil {
		return det, err
	}

	// Extend detection with Go main package discovery
	if det.Language == "go" {
		gb := build.NewGoBuild(false)
		mains, _ := gb.DetectMainPackages(rootDir)
		det.MainPackages = mains
	}

	return det, nil
}

func (e *binaryEngine) Plan(ctx context.Context, cfg build.BuildConfig) ([]build.UniversalStep, error) {
	if cfg.Builder != "go" {
		return nil, fmt.Errorf("binary engine: unsupported builder %q (supported: go)", cfg.Builder)
	}

	if cfg.From == "" {
		return nil, fmt.Errorf("binary engine: from is required")
	}

	// Resolve output artifact name
	binaryName := cfg.Output
	if binaryName == "" {
		// Default: basename of from path
		from := cfg.From
		if strings.HasSuffix(from, ".go") {
			from = filepath.Dir(from)
		}
		binaryName = filepath.Base(from)
	}

	// Resolve template variables in args
	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		args[i] = resolveTemplateVars(a, cfg)
	}

	// Default env
	env := cfg.Env
	if env == nil {
		env = map[string]string{}
	}

	var steps []build.UniversalStep
	for _, tgt := range cfg.Platforms {
		// Physical binary name: append .exe on Windows
		physicalName := binaryName
		if tgt.OS == "windows" {
			physicalName += ".exe"
		}

		// Output path: <DistDir>/{os}-{arch}[-{libc}]/{binary_name}, under
		// .stagefreight/ so binaries ride the perform→publish CI artifact boundary
		// (see build.DistDir). For libc-less targets (all Go), the dir is "os-arch".
		outputPath := fmt.Sprintf("%s/%s/%s", build.DistDir, tgt.Slug(), physicalName)

		stepID := build.StepIDForTarget(cfg.ID, tgt)

		step := build.UniversalStep{
			BuildID: cfg.ID,
			StepID:  stepID,
			Engine:  EngineGo,
			Target:  tgt,
			Outputs: []build.ArtifactRef{
				{Path: outputPath, Type: "binary"},
			},
			Meta: BinaryMeta{
				From:       cfg.From,
				BinaryName: physicalName,
				OutputPath: outputPath,
				Args:       args,
				Env:        env,
				Compress:   cfg.Compress,
			},
		}

		steps = append(steps, step)
	}

	return steps, nil
}

func (e *binaryEngine) ExecuteStep(ctx context.Context, step build.UniversalStep) (*build.UniversalStepResult, error) {
	meta, ok := step.Meta.(BinaryMeta)
	if !ok {
		return nil, fmt.Errorf("binary engine: expected BinaryMeta, got %T", step.Meta)
	}

	start := time.Now()

	// Resolve the Go toolchain via the StageFreight toolchain subsystem — the
	// runtime CI image has no `go` on $PATH, so we can't exec it directly.
	// Resolve downloads + caches a checksummed Go distribution on first use
	// (cache hit thereafter). Mirrors dependency/apply_go.go:resolveGoRunner.
	// rootDir = cwd, which is the job workspace during the build.
	rootDir, _ := os.Getwd()
	goVersion := toolchain.ResolveGoVersion(".", rootDir)
	goRes, err := toolchain.Resolve(rootDir, "go", goVersion)
	if err != nil {
		return nil, fmt.Errorf("binary engine: resolving go toolchain: %w", err)
	}

	gb := build.NewGoBuild(false)

	// Persist the Go module + build caches on the runner's /stagefreight mount so
	// a binary build reuses downloaded modules and compiled packages across CI
	// jobs instead of re-paying the full cold-cache cost (download + cross-compile)
	// every run — the same cross-run reuse the docker/crucible strategy already
	// gets from buildkit cache mounts. Empty dirs (no persistent mount, e.g. local
	// dev) leave Go's $HOME-based defaults untouched. The build's own Env overlays
	// these, so an explicit GOMODCACHE/GOCACHE in config still wins.
	env := map[string]string{}
	if gomod, gocache := toolchain.GoCacheDirs(); gomod != "" {
		env["GOMODCACHE"] = gomod
		env["GOCACHE"] = gocache
	}
	for k, v := range meta.Env {
		env[k] = v
	}

	// Go adapter: the canonical Target projects to GOOS/GOARCH (Libc/ABI are not
	// meaningful for the Go toolchain and are ignored).
	result, err := gb.Build(ctx, build.GoBuildOpts{
		Entry:      meta.From,
		OutputPath: meta.OutputPath,
		GOOS:       step.Target.OS,
		GOARCH:     step.Target.Arch,
		Args:       meta.Args,
		Env:        env,
		GoBin:      goRes.Path,
	})
	if err != nil {
		return nil, err
	}

	return &build.UniversalStepResult{
		Artifacts: []build.ProducedArtifact{
			{
				Path:   result.Path,
				Type:   "binary",
				Size:   result.Size,
				SHA256: result.SHA256,
			},
		},
		Metadata: map[string]string{
			"toolchain":   "go" + goVersion,
			"binary_name": meta.BinaryName,
		},
		Metrics: build.StepMetrics{
			Duration: time.Since(start),
		},
	}, nil
}

// resolveTemplateVars expands template variables in args and similar strings.
func resolveTemplateVars(s string, cfg build.BuildConfig) string {
	if cfg.Version != nil {
		s = strings.ReplaceAll(s, "{version}", cfg.Version.Version)
		s = strings.ReplaceAll(s, "{sha}", cfg.Version.SHA)
		// Support {sha:N} for truncated SHA
		for n := 1; n <= 40; n++ {
			placeholder := fmt.Sprintf("{sha:%d}", n)
			if strings.Contains(s, placeholder) {
				sha := cfg.Version.SHA
				if len(sha) > n {
					sha = sha[:n]
				}
				s = strings.ReplaceAll(s, placeholder, sha)
			}
		}
	}
	s = strings.ReplaceAll(s, "{date}", time.Now().UTC().Format(time.RFC3339))
	return s
}
