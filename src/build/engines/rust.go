package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/substrate"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

// cargoProjectKey derives a stable, readable per-project key for the persistent
// CARGO_TARGET_DIR from the manifest dir (basename for readability + an absolute-path
// hash for uniqueness). Stable across runs because CI checks out at a fixed path.
func cargoProjectKey(manifestDir string) string {
	abs, err := filepath.Abs(manifestDir)
	if err != nil {
		abs = manifestDir
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Base(abs) + "-" + hex.EncodeToString(sum[:])[:8]
}

// EngineRust is the dispatch key for the Rust (cargo) binary engine, sibling to
// binary-go under the generic binary contributor.
const EngineRust = "binary-rust"

func init() {
	build.RegisterV2(EngineRust, func() build.EngineV2 { return &rustEngine{} })
}

// rustEngine compiles Rust binaries with cargo. Like the Go engine, it ONLY produces
// artifacts (Plan + ExecuteStep) — all orchestration, recording, checksums, signing,
// and release live in the shared pipeline, so a Rust binary traverses the exact same
// lifecycle as a Go binary. Host target only for now; cross-compilation is a later
// capability.
type rustEngine struct{}

func (e *rustEngine) Name() string { return EngineRust }

func (e *rustEngine) Capabilities() build.Capabilities {
	return build.Capabilities{
		SupportsCrossCompile: false, // cross via cargo-zigbuild is a later slice
		SupportsCrucible:     false,
		ProducesArchives:     false,
		ProducesOCI:          false,
	}
}

func (e *rustEngine) Detect(ctx context.Context, rootDir string) (*build.Detection, error) {
	// Language detection (Cargo.toml/Cargo.lock) is already generic in DetectRepo.
	return build.DetectRepo(rootDir)
}

func (e *rustEngine) Plan(ctx context.Context, cfg build.BuildConfig) ([]build.UniversalStep, error) {
	if cfg.Builder != "rust" {
		return nil, fmt.Errorf("rust engine: unsupported builder %q (supported: rust)", cfg.Builder)
	}

	manifestDir := cfg.From
	if manifestDir == "" {
		manifestDir = "."
	}
	binName := cfg.Output
	if binName == "" {
		binName = detectCrateBinName(manifestDir)
	}
	if binName == "" {
		return nil, fmt.Errorf("rust engine: cannot determine the binary name — set `output:` to the crate's [[bin]]/package name (no [package].name in %s/Cargo.toml)", manifestDir)
	}

	// Host target only. Cross-compilation (target triples, musl, cargo-zigbuild) is a
	// later capability; a configured non-host platform is rejected rather than silently
	// built for the host.
	host := build.Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	for _, t := range cfg.Platforms {
		if t.OS != host.OS || t.Arch != host.Arch {
			return nil, fmt.Errorf("rust engine: cross-compilation to %s is not yet supported (host-only); remove non-host platforms for now", t.String())
		}
	}

	args := make([]string, len(cfg.Args))
	for i, a := range cfg.Args {
		args[i] = resolveTemplateVars(a, cfg)
	}
	env := cfg.Env
	if env == nil {
		env = map[string]string{}
	}

	outputPath := fmt.Sprintf("%s/%s/%s", build.DistDir, host.Slug(), binName)
	step := build.UniversalStep{
		BuildID: cfg.ID,
		StepID:  build.StepIDForTarget(cfg.ID, host),
		Engine:  EngineRust,
		Target:  host,
		Outputs: []build.ArtifactRef{{Path: outputPath, Type: "binary"}},
		Meta: RustMeta{
			ManifestDir: manifestDir,
			BinName:     binName,
			OutputPath:  outputPath,
			Release:     true,
			Args:        args,
			Env:         env,
		},
	}
	return []build.UniversalStep{step}, nil
}

func (e *rustEngine) ExecuteStep(ctx context.Context, step build.UniversalStep) (*build.UniversalStepResult, error) {
	meta, ok := step.Meta.(RustMeta)
	if !ok {
		return nil, fmt.Errorf("rust engine: expected RustMeta, got %T", step.Meta)
	}
	start := time.Now()

	// Resolve the Rust toolchain via the StageFreight subsystem (verified official
	// dist, no host fallback) — mirrors the Go engine. rootDir = the job workspace.
	rootDir, _ := os.Getwd()
	rustVersion := toolchain.ResolveRustVersion(meta.ManifestDir, rootDir)
	res, err := toolchain.Resolve(rootDir, "rust", rustVersion)
	if err != nil {
		return nil, fmt.Errorf("rust engine: resolving rust toolchain: %w", err)
	}

	// Realize the native build substrate this crate needs (a C toolchain for build-
	// script linking, plus cmake/perl etc. for native crates) — INFERRED from the crate
	// graph, not operator config. The engine emits capabilities; substrate owns
	// realization; the backend owns distro/package semantics. No-op where the tools
	// already exist (dev hosts).
	realized, serr := substrate.NewRealizer(toolchain.SubstrateCacheDir()).
		Realize(ctx, substrate.InferRustNeeds(meta.ManifestDir))
	if serr != nil {
		return nil, fmt.Errorf("rust engine: realizing build substrate: %w", serr)
	}
	substrate.Report(os.Stderr, realized)

	// cargo invokes rustc; with a standalone (non-rustup) install, point it at the
	// sibling rustc and put the toolchain bin on PATH so the build is hermetic.
	binDir := filepath.Dir(res.Path)
	env := map[string]string{
		"RUSTC": filepath.Join(binDir, "rustc"),
		"PATH":  binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	// Persist CARGO_HOME on the /stagefreight mount for cross-run registry/build reuse.
	if ch := toolchain.CargoCacheDir(); ch != "" {
		env["CARGO_HOME"] = ch
	}
	for k, v := range meta.Env {
		env[k] = v
	}

	cb := build.NewCargoBuild(false)
	result, err := cb.Build(ctx, build.CargoBuildOpts{
		ManifestDir: meta.ManifestDir,
		BinName:     meta.BinName,
		OutputPath:  meta.OutputPath,
		Release:     meta.Release,
		Args:        meta.Args,
		Env:         env,
		CargoBin:    res.Path,
		// Persist the compiled target/ across runs (per project) — the Rust analog of
		// GOCACHE. Deps (and their C builds) compile once; only changed local code
		// rebuilds. Turns the cold ~14m recompile into an incremental one.
		TargetDir: toolchain.CargoTargetDir(cargoProjectKey(meta.ManifestDir)),
	})
	if err != nil {
		return nil, err
	}

	return &build.UniversalStepResult{
		Artifacts: []build.ProducedArtifact{
			{Path: result.Path, Type: "binary", Size: result.Size, SHA256: result.SHA256},
		},
		Metadata: map[string]string{
			"toolchain":   "rust" + rustVersion,
			"binary_name": filepath.Base(meta.OutputPath),
		},
		Metrics: build.StepMetrics{Duration: time.Since(start)},
	}, nil
}

// detectCrateBinName reads the crate's [package].name from Cargo.toml — a minimal
// parse sufficient for the common single-binary crate; an explicit `output:` always
// wins. (A full TOML parser / multi-[[bin]] support is a later refinement.)
func detectCrateBinName(manifestDir string) string {
	data, err := os.ReadFile(filepath.Join(manifestDir, "Cargo.toml"))
	if err != nil {
		return ""
	}
	inPackage := false
	for _, line := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") {
			inPackage = l == "[package]"
			continue
		}
		if inPackage && strings.HasPrefix(l, "name") {
			if i := strings.IndexByte(l, '='); i >= 0 {
				return strings.Trim(strings.TrimSpace(l[i+1:]), `"'`)
			}
		}
	}
	return ""
}
