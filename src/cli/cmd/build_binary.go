package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	_ "github.com/PrPlanIT/StageFreight/src/build/engines" // register binary engine
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/runner"
)

var (
	bbLocal     bool
	bbPlatforms []string
	bbBuildID   string
	bbSkipLint  bool
	bbDryRun    bool
	bbOutputDir string
)

var buildBinaryCmd = &cobra.Command{
	Use:   "binary",
	Short: "Build Go binaries",
	Long: `Build Go binaries for configured platforms.

Compiles Go binaries using go build, cross-compiling for all configured platforms.
Injects version, commit, and build date via ldflags.`,
	RunE: runBuildBinary,
}

func init() {
	buildBinaryCmd.Flags().BoolVar(&bbLocal, "local", false, "build for current platform only")
	buildBinaryCmd.Flags().StringSliceVar(&bbPlatforms, "platform", nil, "override platforms (comma-separated)")
	buildBinaryCmd.Flags().StringVar(&bbBuildID, "build", "", "build specific entry by ID (default: all)")
	buildBinaryCmd.Flags().BoolVar(&bbSkipLint, "skip-lint", false, "skip pre-build lint gate")
	buildBinaryCmd.Flags().BoolVar(&bbDryRun, "dry-run", false, "show plan without executing")
	buildBinaryCmd.Flags().StringVar(&bbOutputDir, "output-dir", "", "override output directory")

	buildCmd.AddCommand(buildBinaryCmd)
}

func runBuildBinary(cmd *cobra.Command, args []string) error {
	rootDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	pc := &pipeline.PipelineContext{
		Ctx:           context.Background(),
		RootDir:       rootDir,
		Config:        cfg,
		Writer:        os.Stdout,
		Color:         output.UseColor(),
		CI:            output.IsCI(),
		Verbose:       verbose,
		SkipLint:      bbSkipLint,
		DryRun:        bbDryRun,
		Local:         bbLocal,
		PipelineStart: time.Now(),
		Scratch:       make(map[string]any),
	}

	// Shared v2 state captured by closure across binary execute/archive/publish.
	// Mirrors the docker pipeline pattern in run.go — outputs is populated
	// once per artifact emission, rb is append-only, neither lives in Scratch.
	var outputs artifact.OutputsManifest
	rb := build.NewResultsBuilder()

	p := &pipeline.Pipeline{
		Phases: []pipeline.Phase{
			pipeline.BannerPhase(),
			pipeline.ExecutorPreflightPhase(runner.Options{DockerRequired: false}),
			pipeline.LintPhase(),
			binaryDetectPhase(),
			binaryPlanPhase(),
			pipeline.DryRunGate(renderBinaryPlan),
			binaryExecutePhase(&outputs, rb),
			binaryArchivePhase(&outputs, rb),
			binaryPublishPhase(&outputs, rb),
		},
		Hooks: []pipeline.PostBuildHook{
			postbuild.BadgeHook(cfg, cmdBadgeRunner(cfg)),
		},
	}
	return p.Run(pc)
}

// binaryDetectPhase discovers the repo and filters to binary builds.
func binaryDetectPhase() pipeline.Phase {
	return pipeline.Phase{
		Name: "detect",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			output.SectionStartCollapsed(pc.Writer, "sf_detect", "Detect")
			detectStart := time.Now()

			engine, err := build.GetV2("binary")
			if err != nil {
				output.SectionEnd(pc.Writer, "sf_detect")
				return nil, fmt.Errorf("loading binary engine: %w", err)
			}
			pc.Scratch["binary.engine"] = engine

			det, err := engine.Detect(pc.Ctx, pc.RootDir)
			if err != nil {
				output.SectionEnd(pc.Writer, "sf_detect")
				return nil, fmt.Errorf("detection: %w", err)
			}
			pc.Scratch["binary.det"] = det

			versionInfo, _ := build.DetectVersion(pc.RootDir, pc.Config)
			if versionInfo == nil {
				versionInfo = &gitver.VersionInfo{
					Version: "dev",
					Base:    "0.0.0",
					SHA:     "unknown",
					Branch:  "unknown",
				}
			}
			pc.Scratch["binary.version"] = versionInfo

			// Filter builds to kind: binary
			var binaryBuilds []config.BuildConfig
			for _, b := range pc.Config.Builds {
				if b.Kind != "binary" {
					continue
				}
				if bbBuildID != "" && b.ID != bbBuildID {
					continue
				}
				binaryBuilds = append(binaryBuilds, b)
			}

			detectElapsed := time.Since(detectStart)

			detectSec := output.NewSection(pc.Writer, "Detect", detectElapsed, pc.Color)
			detectSec.Row("%-16s→ %s (auto-detected)", "language", det.Language)
			if len(det.MainPackages) > 0 {
				detectSec.Row("%-16s→ %d package(s)", "main", len(det.MainPackages))
			}
			detectSec.Row("%-16s→ %d configured", "builds", len(binaryBuilds))
			detectSec.Close()
			output.SectionEnd(pc.Writer, "sf_detect")

			if len(binaryBuilds) == 0 {
				if bbBuildID != "" {
					return nil, fmt.Errorf("no binary build found with id %q", bbBuildID)
				}
				// Detection is informational only — binary builds require explicit
				// kind: binary entries in .stagefreight.yml. Log detected mains as
				// a hint for the user.
				hint := "no binary builds defined in config"
				if det.Language == "go" && len(det.MainPackages) > 0 {
					hint += fmt.Sprintf(" (detected %d Go main package(s) — add kind: binary builds to .stagefreight.yml)", len(det.MainPackages))
				}
				return nil, fmt.Errorf("%s", hint)
			}

			pc.Scratch["binary.builds"] = binaryBuilds

			summary := fmt.Sprintf("%s, %d build(s)", det.Language, len(binaryBuilds))
			return &pipeline.PhaseResult{
				Name:    "detect",
				Status:  "success",
				Summary: summary,
				Elapsed: detectElapsed,
			}, nil
		},
	}
}

// binaryPlanPhase plans all binary builds with topological ordering.
func binaryPlanPhase() pipeline.Phase {
	return pipeline.Phase{
		Name: "plan",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			output.SectionStartCollapsed(pc.Writer, "sf_plan", "Plan")
			planStart := time.Now()

			binaryBuilds, ok := pc.Scratch["binary.builds"].([]config.BuildConfig)
			if !ok {
				output.SectionEnd(pc.Writer, "sf_plan")
				return nil, fmt.Errorf("missing binary builds in scratch")
			}
			versionInfo, ok := pc.Scratch["binary.version"].(*gitver.VersionInfo)
			if !ok {
				output.SectionEnd(pc.Writer, "sf_plan")
				return nil, fmt.Errorf("missing binary version in scratch")
			}
			engine, ok := pc.Scratch["binary.engine"].(build.EngineV2)
			if !ok {
				output.SectionEnd(pc.Writer, "sf_plan")
				return nil, fmt.Errorf("missing binary engine in scratch")
			}

			// Fail-fast guardrail: reject non-binary builds
			for _, b := range binaryBuilds {
				if b.Kind != "binary" {
					output.SectionEnd(pc.Writer, "sf_plan")
					return nil, fmt.Errorf("binary plan received non-binary build %q (kind=%s)", b.ID, b.Kind)
				}
			}

			// Topological sort for depends_on ordering
			ordered, err := build.BuildOrder(binaryBuilds)
			if err != nil {
				output.SectionEnd(pc.Writer, "sf_plan")
				return nil, fmt.Errorf("build ordering: %w", err)
			}

			// Plan all builds
			var allSteps []build.UniversalStep
			for _, b := range ordered {
				buildCfg := toBuildConfig(b, versionInfo)

				// CLI overrides
				if pc.Local {
					buildCfg.Platforms = []build.Platform{
						{OS: runtime.GOOS, Arch: runtime.GOARCH},
					}
				}
				if len(bbPlatforms) > 0 {
					buildCfg.Platforms = parsePlatformFlags(bbPlatforms)
				}

				steps, err := engine.Plan(pc.Ctx, buildCfg)
				if err != nil {
					output.SectionEnd(pc.Writer, "sf_plan")
					return nil, fmt.Errorf("planning build %q: %w", b.ID, err)
				}
				allSteps = append(allSteps, steps...)
			}

			// Validate build graph
			if err := build.ValidateBuildGraph(allSteps); err != nil {
				output.SectionEnd(pc.Writer, "sf_plan")
				return nil, fmt.Errorf("build graph validation: %w", err)
			}

			pc.Scratch["binary.steps"] = allSteps

			planElapsed := time.Since(planStart)

			// Collect unique platforms
			seen := make(map[string]bool)
			var platforms []string
			for _, s := range allSteps {
				p := s.Platform.OS + "/" + s.Platform.Arch
				if !seen[p] {
					seen[p] = true
					platforms = append(platforms, p)
				}
			}

			planSec := output.NewSection(pc.Writer, "Plan", planElapsed, pc.Color)
			planSec.Row("%-16s%s", "steps", fmt.Sprintf("%d", len(allSteps)))
			planSec.Row("%-16s%s", "platforms", strings.Join(platforms, ", "))
			planSec.Row("%-16s%s", "version", versionInfo.Version)
			planSec.Close()
			output.SectionEnd(pc.Writer, "sf_plan")

			summary := fmt.Sprintf("%d step(s), %s", len(allSteps), strings.Join(platforms, ","))
			return &pipeline.PhaseResult{
				Name:    "plan",
				Status:  "success",
				Summary: summary,
				Elapsed: planElapsed,
			}, nil
		},
	}
}

// renderBinaryPlan renders the dry-run plan output for binary builds.
func renderBinaryPlan(pc *pipeline.PipelineContext) {
	allSteps, ok := pc.Scratch["binary.steps"].([]build.UniversalStep)
	if !ok {
		return
	}
	fmt.Fprintf(pc.Writer, "Binary build plan (%d steps):\n", len(allSteps))
	for _, s := range allSteps {
		fmt.Fprintf(pc.Writer, "  %-30s  %s/%s  → %s\n",
			s.StepID, s.Platform.OS, s.Platform.Arch,
			formatOutputs(s.Outputs))
	}
}

// binaryExecutePhase compiles all planned binary steps.
// binaryExecutePhase runs each planned binary step and records, per
// successful build, a v2 (intent, outcome) pair via the captured outputs
// pointer and ResultsBuilder.
//
// Recording is pure passthrough — per the Phase 4 unified determinism rule,
// the recording layer takes whatever bytes/metadata the toolchain produced
// and records it verbatim. No path cleanup, no metadata reordering, no
// "harmless" trimming. If the toolchain emitted nondeterministic bytes,
// that is a build-system defect, not something this phase can paper over.
//
// publishedBinaries is kept in Scratch as the in-process handoff for
// binaryArchivePhase to know which binaries to wrap; this is execution-
// internal data flow, not the v2 truth (which lives in outputs + rb).
func binaryExecutePhase(outputs *artifact.OutputsManifest, rb *build.ResultsBuilder) pipeline.Phase {
	return pipeline.Phase{
		Name: "build",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			allSteps, ok := pc.Scratch["binary.steps"].([]build.UniversalStep)
			if !ok {
				return nil, fmt.Errorf("missing binary steps in scratch")
			}
			versionInfo, ok := pc.Scratch["binary.version"].(*gitver.VersionInfo)
			if !ok {
				return nil, fmt.Errorf("missing binary version in scratch")
			}
			engine, ok := pc.Scratch["binary.engine"].(build.EngineV2)
			if !ok {
				return nil, fmt.Errorf("missing binary engine in scratch")
			}

			output.SectionStart(pc.Writer, "sf_build", "Build")
			buildStart := time.Now()

			// Local in-process handoff to binaryArchivePhase. NOT v2 truth
			// (which lives in outputs + rb); this is intra-pipeline data
			// flow for the archive phase to know which files to wrap.
			var publishedBinaries []builtBinary

			buildSec := output.NewSection(pc.Writer, "Build", 0, pc.Color)
			for _, step := range allSteps {
				result, err := engine.ExecuteStep(pc.Ctx, step)
				if err != nil {
					buildSec.Row("%-30s  %s/%s  %s", step.StepID, step.Platform.OS, step.Platform.Arch,
						output.StatusIcon("failed", pc.Color))
					buildSec.Close()
					output.SectionEnd(pc.Writer, "sf_build")
					return &pipeline.PhaseResult{
						Name:    "build",
						Status:  "failed",
						Summary: fmt.Sprintf("step %s: %v", step.StepID, err),
						Elapsed: time.Since(buildStart),
					}, fmt.Errorf("step %s: %w", step.StepID, err)
				}

				buildSec.Row("%-30s  %s/%s  %s  (%.1fs)", step.StepID,
					step.Platform.OS, step.Platform.Arch,
					output.StatusIcon("success", pc.Color),
					result.Metrics.Duration.Seconds())

				binaryName := result.Metadata["binary_name"]
				toolchain := result.Metadata["toolchain"]

				for _, out := range result.Artifacts {
					// Plan-time intent: descriptor describes the binary
					// without execution observations (no SHA256, no Size).
					// Per Q2: no targets.
					artifactName := uniqueBinaryArtifactName(binaryName, step.Platform.OS, step.Platform.Arch)
					artifactID := artifact.NewArtifactID("binary", artifactName)
					outputs.Artifacts = append(outputs.Artifacts, artifact.Artifact{
						Kind:    "binary",
						Name:    artifactName,
						Version: versionInfo.Version,
						Binary: &artifact.BinaryDescriptor{
							OS:        step.Platform.OS,
							Arch:      step.Platform.Arch,
							Path:      out.Path,
							Toolchain: toolchain,
						},
					})

					// Execution observation: BinaryOutcome carries whatever
					// the build emitted. No target (un-targeted by design).
					rb.Record(artifactID, artifact.Outcome{
						Type: artifact.OutcomeTypeBinaryBuild,
						Binary: &artifact.BinaryOutcome{
							Status:  artifact.OutcomeSuccess,
							SHA256:  out.SHA256,
							Path:    out.Path,
							Size:    out.Size,
							BuildID: step.BuildID,
						},
					})

					// In-process handoff for binaryArchivePhase. Not part
					// of v2 truth — that's already recorded above via rb.
					publishedBinaries = append(publishedBinaries, builtBinary{
						Name:    binaryName,
						OS:      step.Platform.OS,
						Arch:    step.Platform.Arch,
						Path:    out.Path,
						SHA256:  out.SHA256,
						Size:    out.Size,
						BuildID: step.BuildID,
					})
				}
			}
			buildElapsed := time.Since(buildStart)
			buildSec.Close()
			output.SectionEnd(pc.Writer, "sf_build")

			pc.Scratch["binary.published"] = publishedBinaries

			summary := fmt.Sprintf("%d binary(ies)", len(publishedBinaries))
			return &pipeline.PhaseResult{
				Name:    "build",
				Status:  "success",
				Summary: summary,
				Elapsed: buildElapsed,
			}, nil
		},
	}
}

// builtBinary is the in-process handoff between binaryExecutePhase and
// binaryArchivePhase. It carries the fields the archive phase needs
// (which file to wrap, what its build ID/platform/etc. are) WITHOUT
// using any v1 type. The v2 truth for these binaries is already
// recorded in rb at the moment of execution; this struct exists purely
// as intra-pipeline data flow.
type builtBinary struct {
	Name    string
	OS      string
	Arch    string
	Path    string
	SHA256  string
	Size    int64
	BuildID string
}

// uniqueBinaryArtifactName composes a stable, unique-per-build artifact
// name for binaries across multiple platforms. Cross-platform builds
// produce multiple binaries with the same logical name; we suffix the
// platform so each (binary, platform) tuple has a distinct ArtifactID.
//
// Determinism rule applies: this name is identity, not observation, so it
// must be derived deterministically from plan inputs only.
func uniqueBinaryArtifactName(binaryName, osName, arch string) string {
	return binaryName + "-" + osName + "-" + arch
}

// binaryArchivePhase creates archives for configured binary-archive targets
// and emits ArchiveOutcome facts via the supplied ResultsBuilder. Each
// archive is a sibling artifact to its source binaries — never embedded.
// Sources references binary ArtifactIDs by string; sorting happens at
// ResultsManifest.Finalize time, not here.
//
// CreateArchive enforces archive-bytes determinism (sorted entry order,
// normalized tar/zip headers, hash over final stream). This phase just
// records what CreateArchive emitted.
func binaryArchivePhase(outputs *artifact.OutputsManifest, rb *build.ResultsBuilder) pipeline.Phase {
	return pipeline.Phase{
		Name: "archive",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			publishedBinaries, ok := pc.Scratch["binary.published"].([]builtBinary)
			if !ok {
				return nil, fmt.Errorf("missing binary.published in scratch")
			}
			versionInfo, ok := pc.Scratch["binary.version"].(*gitver.VersionInfo)
			if !ok {
				return nil, fmt.Errorf("missing binary version in scratch")
			}

			archiveTargets := pipeline.CollectTargetsByKind(pc.Config, "binary-archive")
			if len(archiveTargets) == 0 {
				return &pipeline.PhaseResult{
					Name:    "archive",
					Status:  "skipped",
					Summary: "no archive targets",
				}, nil
			}

			output.SectionStartCollapsed(pc.Writer, "sf_archive", "Archive")
			archiveStart := time.Now()

			// Track per-archive results for checksums + summary count.
			// Local type since archive identity (v2) is already recorded
			// via rb above; this slice exists only to drive the per-target
			// SHA256SUMS sidecar file.
			var archiveCount int
			archiveSec := output.NewSection(pc.Writer, "Archive", 0, pc.Color)

			for _, t := range archiveTargets {
				var targetArchives []*build.ArchiveResult

				for _, pb := range publishedBinaries {
					if t.Build != pb.BuildID {
						continue
					}

					archiveBinaryName := t.BinaryName
					if archiveBinaryName == "" {
						archiveBinaryName = pb.Name
					}

					nameTemplate := t.Name
					if nameTemplate == "" {
						nameTemplate = "{id}-{version}-{os}-{arch}"
					}

					archResult, err := build.CreateArchive(build.ArchiveOpts{
						Format:       t.Format,
						OutputDir:    filepath.Join(pc.RootDir, "dist"),
						NameTemplate: nameTemplate,
						BinaryPath:   pb.Path,
						BinaryName:   archiveBinaryName,
						IncludeFiles: t.Include,
						RepoRoot:     pc.RootDir,
						Platform:     build.Platform{OS: pb.OS, Arch: pb.Arch},
						BuildID:      pb.BuildID,
						Version:      versionInfo,
					})
					if err != nil {
						archiveSec.Close()
						output.SectionEnd(pc.Writer, "sf_archive")
						return nil, fmt.Errorf("archive for %s/%s: %w", pb.OS, pb.Arch, err)
					}

					archiveSec.Row("%-40s %s  (%s, %d bytes)",
						filepath.Base(archResult.Path),
						output.StatusIcon("success", pc.Color),
						archResult.Format, archResult.Size)

					// v2 intent + outcome emission. Archive name uses the
					// archive filename basename so the artifact_id stays
					// stable across the (binary, archive) sibling pair.
					archiveArtifactName := filepath.Base(archResult.Path)
					archiveArtifactID := artifact.NewArtifactID("archive", archiveArtifactName)
					outputs.Artifacts = append(outputs.Artifacts, artifact.Artifact{
						Kind:    "archive",
						Name:    archiveArtifactName,
						Version: versionInfo.Version,
						Archive: &artifact.ArchiveDescriptor{
							Format: archResult.Format,
							Path:   archResult.Path,
						},
					})

					// ArchiveOutcome carries the bytes-deterministic SHA256
					// from CreateArchive plus the Sources sibling reference
					// to the source binary's ArtifactID. Sources is
					// semantically unordered; ResultsManifest.Finalize sorts
					// it for canonical serialization.
					sourceBinaryID := artifact.NewArtifactID("binary", uniqueBinaryArtifactName(pb.Name, pb.OS, pb.Arch))
					rb.Record(archiveArtifactID, artifact.Outcome{
						Type: artifact.OutcomeTypeArchive,
						Archive: &artifact.ArchiveOutcome{
							Status:  artifact.OutcomeSuccess,
							SHA256:  archResult.SHA256,
							Path:    archResult.Path,
							Format:  archResult.Format,
							Size:    archResult.Size,
							Sources: []artifact.ArtifactID{sourceBinaryID},
						},
					})

					targetArchives = append(targetArchives, archResult)
				}

				// Write checksums scoped to this target's archives only.
				if t.Checksums && len(targetArchives) > 0 {
					checksumPath, err := build.WriteChecksums(filepath.Join(pc.RootDir, "dist"), targetArchives)
					if err != nil {
						archiveSec.Close()
						output.SectionEnd(pc.Writer, "sf_archive")
						return nil, fmt.Errorf("writing checksums: %w", err)
					}
					archiveSec.Row("%-40s %s  checksums", filepath.Base(checksumPath),
						output.StatusIcon("success", pc.Color))
				}

				archiveCount += len(targetArchives)
			}

			archiveElapsed := time.Since(archiveStart)
			archiveSec.Close()
			output.SectionEnd(pc.Writer, "sf_archive")

			summary := fmt.Sprintf("%d archive(s)", archiveCount)
			return &pipeline.PhaseResult{
				Name:    "archive",
				Status:  "success",
				Summary: summary,
				Elapsed: archiveElapsed,
			}, nil
		},
	}
}

// binaryPublishPhase is the pure flush sink for the binary pipeline.
// Mirrors docker's publishPhase: writes outputs.json and published.json
// from the in-memory OutputsManifest + ResultsBuilder. No Scratch reads,
// no v1 publishManifest, no publication decision logic.
//
// Skipped when no artifacts were emitted — same semantic as the docker
// publish phase: publication occurred iff outputs.Artifacts is non-empty.
func binaryPublishPhase(outputs *artifact.OutputsManifest, rb *build.ResultsBuilder) pipeline.Phase {
	return pipeline.Phase{
		Name: "publish",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			if outputs == nil || len(outputs.Artifacts) == 0 {
				return &pipeline.PhaseResult{
					Name:    "publish",
					Status:  "skipped",
					Summary: "no artifacts",
				}, nil
			}

			// Freeze the intent identity: Finalize computes outputs.Checksum over
			// the now-complete artifact set (binaries from execute + archives from
			// archive). The docker path does this before results are built; the
			// binary path must too, or rb.Build rejects an unchecksummed manifest.
			if err := outputs.Finalize(); err != nil {
				return nil, fmt.Errorf("finalizing outputs manifest: %w", err)
			}

			if err := artifact.WriteOutputsManifest(pc.RootDir, *outputs); err != nil {
				return nil, fmt.Errorf("writing outputs manifest: %w", err)
			}

			results, err := rb.Build(outputs)
			if err != nil {
				return nil, fmt.Errorf("building results manifest: %w", err)
			}
			if err := artifact.WriteResultsManifest(pc.RootDir, results); err != nil {
				return nil, fmt.Errorf("writing results manifest: %w", err)
			}

			outcomeCount := 0
			for _, r := range results.Results {
				outcomeCount += len(r.Outcomes)
			}
			return &pipeline.PhaseResult{
				Name:   "publish",
				Status: "success",
				Summary: fmt.Sprintf("%d outcome(s) across %d artifact(s)",
					outcomeCount, len(results.Results)),
			}, nil
		},
	}
}

// toBuildConfig converts a config.BuildConfig to a build.BuildConfig for the engine.
func toBuildConfig(b config.BuildConfig, v *gitver.VersionInfo) build.BuildConfig {
	platforms := build.ParsePlatforms(b.Platforms)
	if len(platforms) == 0 {
		platforms = []build.Platform{
			{OS: runtime.GOOS, Arch: runtime.GOARCH},
		}
	}

	return build.BuildConfig{
		ID:         b.ID,
		Kind:       b.Kind,
		Platforms:  platforms,
		BuildMode:  b.BuildMode,
		SelectTags: b.SelectTags,
		DependsOn:  b.DependsOn,
		Version:    v,
		Builder:    b.Builder,
		Command:    b.BuilderCommand(),
		From:       b.From,
		Output:     b.OutputName(),
		Args:       b.Args,
		Env:        b.Env,
		Compress:   b.Compress,
	}
}

// parsePlatformFlags converts CLI platform strings to Platform structs.
func parsePlatformFlags(flags []string) []build.Platform {
	var platforms []build.Platform
	for _, f := range flags {
		for _, p := range strings.Split(f, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				platforms = append(platforms, build.ParsePlatform(p))
			}
		}
	}
	return platforms
}

func formatOutputs(refs []build.ArtifactRef) string {
	var paths []string
	for _, r := range refs {
		paths = append(paths, r.Path)
	}
	return strings.Join(paths, ", ")
}

// cmdBadgeRunner returns a postbuild.BadgeRunner that uses cmd-local badge helpers.
func cmdBadgeRunner(appCfg *config.Config) postbuild.BadgeRunner {
	return func(w io.Writer, color bool, rootDir string) (string, time.Duration) {
		start := time.Now()
		err := RunConfigBadges(appCfg, rootDir, nil, "passed")
		elapsed := time.Since(start)
		if err != nil {
			return fmt.Sprintf("error: %v", err), elapsed
		}
		items := postbuild.CollectNarratorBadgeItems(appCfg)
		return fmt.Sprintf("%d generated", len(items)), elapsed
	}
}
