// Package contributors holds the build-strategy contributors that supply rows
// into a perform run's domains. A contributor owns a capability (Go binary
// compilation, here); the domain runner owns execution order and presentation.
package contributors

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	_ "github.com/PrPlanIT/StageFreight/src/build/engines" // register the binary EngineV2
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/sign/autosign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
	"github.com/PrPlanIT/StageFreight/src/toolchain"
)

func init() {
	domains.RegisterContributor(func() domains.Contributor { return &binaryContributor{} })
}

// binaryContributor compiles Go binaries (kind: binary) and archives them
// (kind: binary-archive). It joins Detect, Plan, Build and Publish. Its state is
// run-scoped: one instance per run threads detection → plan → build → archive.
type binaryContributor struct {
	engine  build.EngineV2
	builds  []config.BuildConfig
	version *gitver.VersionInfo
	steps   []build.UniversalStep
	built   []builtBinary
}

func (b *binaryContributor) Name() string { return "binary" }
func (b *binaryContributor) Order() int   { return 10 }

func (b *binaryContributor) Applies(rc *domains.RunContext) bool {
	for _, bld := range rc.Config.Builds {
		if bld.Kind == "binary" {
			return true
		}
	}
	return false
}

// Detect resolves the binary engine, the version, and the binary builds.
func (b *binaryContributor) Detect(rc *domains.RunContext) (domains.Contribution, error) {
	eng, err := build.GetV2("binary")
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("loading binary engine: %w", err)
	}
	b.engine = eng

	det, err := eng.Detect(rc.Ctx, rc.RootDir)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("binary detection: %w", err)
	}

	v, _ := build.DetectVersion(rc.RootDir, rc.Config)
	if v == nil {
		// No repo / no tag lineage: keep the real CI commit + branch, not "unknown".
		v = gitver.SyntheticVersion()
	}
	b.version = v

	for _, bc := range rc.Config.Builds {
		if bc.Kind != "binary" {
			continue
		}
		if rc.BuildID != "" && bc.ID != rc.BuildID {
			continue
		}
		b.builds = append(b.builds, bc)
	}

	row := fmt.Sprintf("%-9s %s · %d build(s)", "binary", det.Language, len(b.builds))
	if len(det.MainPackages) > 0 {
		row += fmt.Sprintf(", %d main package(s)", len(det.MainPackages))
	}
	return domains.Contribution{
		Rows:    []string{row},
		Status:  "success",
		Summary: fmt.Sprintf("go, %d binary build(s)", len(b.builds)),
	}, nil
}

// Plan plans all binary builds (topologically ordered), applying CLI overrides.
func (b *binaryContributor) Plan(rc *domains.RunContext) (domains.Contribution, error) {
	ordered, err := build.BuildOrder(b.builds)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("binary build ordering: %w", err)
	}

	for _, bc := range ordered {
		cfg := toBuildConfig(bc, b.version)
		if rc.Local {
			cfg.Platforms = []build.Platform{{OS: runtime.GOOS, Arch: runtime.GOARCH}}
		}
		if len(rc.Platforms) > 0 {
			cfg.Platforms = parsePlatformFlags(rc.Platforms)
		}
		steps, err := b.engine.Plan(rc.Ctx, cfg)
		if err != nil {
			return domains.Contribution{}, fmt.Errorf("planning binary build %q: %w", bc.ID, err)
		}
		b.steps = append(b.steps, steps...)
	}
	if err := build.ValidateBuildGraph(b.steps); err != nil {
		return domains.Contribution{}, fmt.Errorf("binary build graph: %w", err)
	}

	platforms := uniquePlatforms(b.steps)
	row := fmt.Sprintf("%-9s %d step(s) · %s", "binary", len(b.steps), strings.Join(platforms, ", "))
	return domains.Contribution{
		Rows:    []string{row},
		Status:  "success",
		Summary: fmt.Sprintf("%d binary step(s)", len(b.steps)),
	}, nil
}

// Build compiles every planned step, recording each binary into the shared
// run manifest (rc.Outputs/rc.RB) and returning one row per platform.
func (b *binaryContributor) Build(rc *domains.RunContext) (domains.Contribution, error) {
	if len(b.steps) == 0 {
		return domains.Contribution{Skip: true}, nil
	}

	// Lead with the Go cache disposition so cross-run reuse is observable, not
	// inferred from build times. "off" means /stagefreight is unwritable and the
	// build is paying the full cold cost into ephemeral storage every run; "warm"
	// means the persistent GOCACHE was reused; "cold" means this run is populating
	// it for the next. Mirrors the docker strategy's cache row.
	var rows []string
	if gomod, _, warm := toolchain.GoCacheStatus(); gomod == "" {
		rows = append(rows, fmt.Sprintf("%-9s cache off — /stagefreight not writable (ephemeral $HOME)", "binary"))
	} else {
		state := "cold (populating)"
		if warm {
			state = "warm (reused)"
		}
		rows = append(rows, fmt.Sprintf("%-9s cache %s · %s", "binary", filepath.Dir(gomod), state))
	}

	for i := range b.steps {
		step := b.steps[i]
		result, err := b.engine.ExecuteStep(rc.Ctx, step)
		if err != nil {
			rows = append(rows, fmt.Sprintf("%-9s %-30s %s/%s  %s",
				"binary", step.StepID, step.Platform.OS, step.Platform.Arch,
				output.StatusIcon("failed", rc.Color)))
			return domains.Contribution{Rows: rows, Status: "failed", Summary: "binary build failed"},
				fmt.Errorf("binary step %s: %w", step.StepID, err)
		}

		rows = append(rows, fmt.Sprintf("%-9s %-30s %s/%s  %s  (%.1fs)",
			"binary", step.StepID, step.Platform.OS, step.Platform.Arch,
			output.StatusIcon("success", rc.Color), result.Metrics.Duration.Seconds()))

		binaryName := result.Metadata["binary_name"]
		toolchainID := result.Metadata["toolchain"] // local; avoid shadowing the toolchain package
		for _, out := range result.Artifacts {
			artifactName := uniqueBinaryArtifactName(binaryName, step.Platform.OS, step.Platform.Arch)
			artifactID := artifact.NewArtifactID("binary", artifactName)
			rc.Outputs.Artifacts = append(rc.Outputs.Artifacts, artifact.Artifact{
				Kind:    "binary",
				Name:    artifactName,
				Version: b.version.Version,
				Binary: &artifact.BinaryDescriptor{
					OS: step.Platform.OS, Arch: step.Platform.Arch, Path: out.Path, Toolchain: toolchainID,
				},
			})
			rc.RB.Record(artifactID, artifact.Outcome{
				Type: artifact.OutcomeTypeBinaryBuild,
				Binary: &artifact.BinaryOutcome{
					Status: artifact.OutcomeSuccess, SHA256: out.SHA256, Path: out.Path,
					Size: out.Size, BuildID: step.BuildID,
				},
			})
			b.built = append(b.built, builtBinary{
				Name: binaryName, OS: step.Platform.OS, Arch: step.Platform.Arch,
				Path: out.Path, SHA256: out.SHA256, Size: out.Size, BuildID: step.BuildID,
			})
		}
	}
	return domains.Contribution{
		Rows:    rows,
		Status:  "success",
		Summary: fmt.Sprintf("%d binary(ies)", len(b.built)),
	}, nil
}

// Publish archives the built binaries for configured binary-archive targets and
// records each archive (plus SHA256SUMS) into the shared run manifest.
func (b *binaryContributor) Publish(rc *domains.RunContext) (domains.Contribution, error) {
	archiveTargets := pipeline.CollectTargetsByKind(rc.Config, "binary-archive")
	if len(archiveTargets) == 0 || len(b.built) == 0 {
		return domains.Contribution{Skip: true}, nil
	}

	var rows []string
	archiveCount := 0
	for _, t := range archiveTargets {
		// Gate on when: — a binary-archive only builds for its configured event/
		// branch/tag (e.g. a dev archive on main-push, a stable archive on tag).
		if !config.TargetMatchesEnv(t, rc.Config) {
			continue
		}
		var targetArchives []*build.ArchiveResult
		for _, pb := range b.built {
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
				OutputDir:    filepath.Join(rc.RootDir, build.DistDir),
				NameTemplate: nameTemplate,
				BinaryPath:   pb.Path,
				BinaryName:   archiveBinaryName,
				IncludeFiles: t.Include,
				RepoRoot:     rc.RootDir,
				Platform:     build.Platform{OS: pb.OS, Arch: pb.Arch},
				BuildID:      pb.BuildID,
				Version:      b.version,
			})
			if err != nil {
				return domains.Contribution{Rows: rows, Status: "failed", Summary: "archive failed"},
					fmt.Errorf("archive for %s/%s: %w", pb.OS, pb.Arch, err)
			}

			rows = append(rows, fmt.Sprintf("%-9s %-40s %s  (%s, %d bytes)",
				"binary", filepath.Base(archResult.Path),
				output.StatusIcon("success", rc.Color), archResult.Format, archResult.Size))

			archiveArtifactName := filepath.Base(archResult.Path)
			archiveArtifactID := artifact.NewArtifactID("archive", archiveArtifactName)
			rc.Outputs.Artifacts = append(rc.Outputs.Artifacts, artifact.Artifact{
				Kind:    "archive",
				Name:    archiveArtifactName,
				Version: b.version.Version,
				Archive: &artifact.ArchiveDescriptor{Format: archResult.Format, Path: archResult.Path},
			})
			sourceBinaryID := artifact.NewArtifactID("binary", uniqueBinaryArtifactName(pb.Name, pb.OS, pb.Arch))
			rc.RB.Record(archiveArtifactID, artifact.Outcome{
				Type: artifact.OutcomeTypeArchive,
				Archive: &artifact.ArchiveOutcome{
					Status: artifact.OutcomeSuccess, SHA256: archResult.SHA256, Path: archResult.Path,
					Format: archResult.Format, Size: archResult.Size,
					Sources: []artifact.ArtifactID{sourceBinaryID},
				},
			})
			targetArchives = append(targetArchives, archResult)
		}

		if t.Checksums && len(targetArchives) > 0 {
			checksumPath, err := build.WriteChecksums(filepath.Join(rc.RootDir, build.DistDir), targetArchives)
			if err != nil {
				return domains.Contribution{Rows: rows, Status: "failed", Summary: "checksums failed"},
					fmt.Errorf("writing checksums: %w", err)
			}
			rows = append(rows, fmt.Sprintf("%-9s %-40s %s  checksums",
				"binary", filepath.Base(checksumPath), output.StatusIcon("success", rc.Color)))

			// Sign the checksum bundle when the target names an explicit
			// signing_profile — the trust anchor for every archive it covers.
			row, serr := b.signChecksums(rc, t, checksumPath)
			if row != "" {
				rows = append(rows, row)
			}
			if serr != nil {
				return domains.Contribution{Rows: rows, Status: "failed", Summary: "checksum signing failed"}, serr
			}
		}
		archiveCount += len(targetArchives)
	}

	return domains.Contribution{
		Rows:    rows,
		Status:  "success",
		Summary: fmt.Sprintf("%d archive(s)", archiveCount),
	}, nil
}

// signChecksums signs SHA256SUMS with cosign when the target names an EXPLICIT
// signing_profile. The synthesized `legacy` default never auto-signs blobs (locked
// back-compat decision) — so an empty signing_profile is a deliberate skip. Returns
// a status row (or "") and a fatal error: by default a signing failure is recorded
// + surfaced but non-fatal (Publish owns block-vs-proceed); a profile with
// `enforce: true` makes it fatal. The recorded outcome carries the resolved trust
// evidence, never a bare signed=true.
func (b *binaryContributor) signChecksums(rc *domains.RunContext, t config.TargetConfig, checksumPath string) (string, error) {
	cfg := rc.Config.SigningSetup
	if !cfg.SigningEnabled() {
		return "", nil // global kill switch
	}

	// Explicit profile if the target names one; otherwise nil — checksums sign
	// without a profile ONLY under consented Tier-0 auto-provision (the legacy
	// default never auto-signs blobs).
	var profile *config.ResolvedSigningProfile
	if t.SigningProfile != "" {
		p, err := config.ResolveSigningProfileForTarget(t, rc.Config.Signing)
		if err != nil {
			diag.Warn("checksum signing for %s: %v", t.ID, err)
			return "", nil
		}
		profile = p
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sc, serr := autosign.ResolveSigningContext(rc.Ctx, cfg, profile, rc.RootDir, rc.RootDir, rc.Config.Toolchains.Desired, now)
	if serr != nil {
		return "", serr // FATAL (continuity / state-dir guard)
	}
	if !sc.DoSign {
		return "", nil // no profile + no consented Tier-0 → deliberate skip
	}
	enforce := profile != nil && profile.Enforce

	artifactID := artifact.NewArtifactID("checksums", filepath.Base(checksumPath))
	evidence := sc.Evidence(now)
	sigPath := checksumPath + ".sig"
	err := cosign.SignBlob(rc.Ctx, rc.RootDir, rc.Config.Toolchains.Desired, checksumPath, sigPath, sc.Plan, sc.Env)
	if err != nil {
		rc.RB.Record(artifactID, artifact.Outcome{
			Type: artifact.OutcomeTypeBlobSignature,
			BlobSignature: &artifact.BlobSignatureOutcome{
				Status: artifact.OutcomeFailed, Kind: "cosign",
				BlobPath: checksumPath, TrustEvidence: evidence, Error: err.Error(),
			},
		})
		row := fmt.Sprintf("%-9s %-40s %s  signature failed",
			"binary", filepath.Base(checksumPath)+".sig", output.StatusIcon("failed", rc.Color))
		if enforce {
			return row, fmt.Errorf("signing checksums for %s (enforce): %w", t.ID, err)
		}
		return row, nil
	}

	// The detached signature lands in DistDir beside SHA256SUMS (so release upload
	// ships it); its authoritative record is the blob_signature outcome below.
	rc.RB.Record(artifactID, artifact.Outcome{
		Type: artifact.OutcomeTypeBlobSignature,
		BlobSignature: &artifact.BlobSignatureOutcome{
			Status: artifact.OutcomeSuccess, Kind: "cosign",
			BlobPath: checksumPath, SignaturePath: sigPath, TrustEvidence: evidence,
		},
	})
	return fmt.Sprintf("%-9s %-40s %s  signature",
		"binary", filepath.Base(sigPath), output.StatusIcon("success", rc.Color)), nil
}

// ── relocated binary helpers (were cmd-local) ────────────────────────────────

type builtBinary struct {
	Name    string
	OS      string
	Arch    string
	Path    string
	SHA256  string
	Size    int64
	BuildID string
}

func uniqueBinaryArtifactName(binaryName, osName, arch string) string {
	return binaryName + "-" + osName + "-" + arch
}

func toBuildConfig(b config.BuildConfig, v *gitver.VersionInfo) build.BuildConfig {
	platforms := build.ParsePlatforms(b.Platforms)
	if len(platforms) == 0 {
		platforms = []build.Platform{{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	}
	return build.BuildConfig{
		ID: b.ID, Kind: b.Kind, Platforms: platforms, BuildMode: b.BuildMode,
		SelectTags: b.SelectTags, DependsOn: b.DependsOn, Version: v,
		Builder: b.Builder, Command: b.BuilderCommand(), From: b.From,
		Output: b.OutputName(), Args: b.Args, Env: b.Env, Compress: b.Compress,
	}
}

func parsePlatformFlags(flags []string) []build.Platform {
	var platforms []build.Platform
	for _, f := range flags {
		for _, p := range strings.Split(f, ",") {
			if p = strings.TrimSpace(p); p != "" {
				platforms = append(platforms, build.ParsePlatform(p))
			}
		}
	}
	return platforms
}

func uniquePlatforms(steps []build.UniversalStep) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range steps {
		p := s.Platform.OS + "/" + s.Platform.Arch
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}
