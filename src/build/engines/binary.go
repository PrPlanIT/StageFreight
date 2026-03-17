package engines

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
)

func init() {
	build.RegisterV2("binary", func() build.EngineV2 { return &binaryEngine{} })
}

// binaryEngine compiles Go binaries. ~150 lines. Plan + ExecuteStep only.
// All orchestration (ordering, concurrency, logging, artifact recording,
// checksums, publish manifest) lives in core.
type binaryEngine struct{}

func (e *binaryEngine) Name() string { return "binary" }

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
	if cfg.Language != "go" {
		return nil, fmt.Errorf("binary engine: unsupported language %q (supported: go)", cfg.Language)
	}

	if cfg.Entry == "" {
		return nil, fmt.Errorf("binary engine: entry is required")
	}

	// Resolve binary name
	binaryName := cfg.BinaryName
	if binaryName == "" {
		// Default: basename of entry directory
		entry := cfg.Entry
		if strings.HasSuffix(entry, ".go") {
			entry = filepath.Dir(entry)
		}
		binaryName = filepath.Base(entry)
	}

	// Resolve output template
	outputTmpl := cfg.Output
	if outputTmpl == "" {
		outputTmpl = "dist/{os}-{arch}/{binary_name}"
	}

	// Resolve ldflags templates
	ldflags := make([]string, len(cfg.LDFlags))
	for i, f := range cfg.LDFlags {
		ldflags[i] = resolveTemplateVars(f, cfg)
	}

	// Default env
	env := cfg.Env
	if env == nil {
		env = map[string]string{}
	}

	var steps []build.UniversalStep
	for _, plat := range cfg.Platforms {
		// Physical binary name: append .exe on Windows
		physicalName := binaryName
		if plat.OS == "windows" {
			physicalName += ".exe"
		}

		// Resolve output path
		outputPath := resolveOutputPath(outputTmpl, cfg.ID, plat, binaryName, physicalName, cfg.Version)

		stepID := build.StepIDForPlatform(cfg.ID, plat)

		step := build.UniversalStep{
			BuildID:  cfg.ID,
			StepID:   stepID,
			Engine:   "binary",
			Platform: plat,
			Outputs: []build.ArtifactRef{
				{Path: outputPath, Type: "binary"},
			},
			Meta: BinaryMeta{
				Entry:      cfg.Entry,
				BinaryName: physicalName,
				OutputPath: outputPath,
				LDFlags:    ldflags,
				Env:        env,
				Strip:      cfg.Strip,
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

	gb := build.NewGoBuild(false)

	// Resolve ldflags with strip flag
	ldflags := meta.LDFlags
	if meta.Strip {
		// Prepend -s -w if not already present
		hasStrip := false
		for _, f := range ldflags {
			if strings.Contains(f, "-s") && strings.Contains(f, "-w") {
				hasStrip = true
				break
			}
		}
		if !hasStrip {
			ldflags = append([]string{"-s -w"}, ldflags...)
		}
	}

	// Get toolchain version for metadata
	toolchain, _ := gb.ToolchainVersion(ctx)

	result, err := gb.Build(ctx, build.GoBuildOpts{
		Entry:      meta.Entry,
		OutputPath: meta.OutputPath,
		GOOS:       step.Platform.OS,
		GOARCH:     step.Platform.Arch,
		LDFlags:    ldflags,
		Env:        meta.Env,
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
			"toolchain":   toolchain,
			"binary_name": meta.BinaryName,
		},
		Metrics: build.StepMetrics{
			Duration: time.Since(start),
		},
	}, nil
}

// resolveOutputPath expands the output template for a specific platform.
func resolveOutputPath(tmpl, buildID string, plat build.Platform, binaryName, physicalName string, v *build.VersionInfo) string {
	s := tmpl
	s = strings.ReplaceAll(s, "{id}", buildID)
	s = strings.ReplaceAll(s, "{os}", plat.OS)
	s = strings.ReplaceAll(s, "{arch}", plat.Arch)
	s = strings.ReplaceAll(s, "{binary_name}", physicalName)
	if v != nil {
		s = strings.ReplaceAll(s, "{version}", v.Version)
	}
	return s
}

// resolveTemplateVars expands template variables in ldflags and similar strings.
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
