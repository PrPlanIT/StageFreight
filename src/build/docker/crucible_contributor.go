package docker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/version"
)

func init() {
	domains.RegisterContributor(func() domains.Contributor { return &crucibleContributor{} })
}

// crucibleContributor is the docker/crucible build strategy as a domain
// contributor. It is the decomposition of the former runCrucibleMode monolith:
// the SAME trust-critical sequence (candidate → self-proof → VerifyCrucible →
// gated publish), the SAME helpers (executeBuildPass, VerifyCrucible,
// captureArtifactDigests, persistArtifacts) called verbatim. What changed is
// ownership: it joins the run's domain spine instead of owning its own, and it
// records into the run's shared Outputs/RB instead of writing its own manifest
// pair. Its Builder/Cache/pass/Content-Store/provenance/verdict boxes still
// render as they did — folding those into subsections is a later, presentation-
// only pass; nothing trust-critical moves here.
type crucibleContributor struct {
	req           Request
	rootDir       string
	runID         string
	candidateTag  string
	verifyTag     string
	created       string
	pipelineStart time.Time
	backend       *Backend
	engine        build.Engine
	det           *build.Detection
	plan          *build.BuildPlan
	builderInfo   BuilderInfo
	cacheTo       []build.CacheRef

	cruciblePassed bool
	verification   *CrucibleVerification
}

func (c *crucibleContributor) Name() string { return "docker" }
func (c *crucibleContributor) Order() int   { return 20 }

func (c *crucibleContributor) Applies(rc *domains.RunContext) bool {
	for _, b := range rc.Config.Builds {
		if b.Kind == "docker" && b.BuildMode == "crucible" {
			if rc.BuildID == "" || b.ID == rc.BuildID {
				return true
			}
		}
	}
	return false
}

// SubstrateNeeds: crucible always needs Docker with crucible-strength thresholds.
func (c *crucibleContributor) NeedsDocker() bool   { return true }
func (c *crucibleContributor) NeedsCrucible() bool { return true }

// Detect resolves the crucible run identity (run id, tags, backend) and detects
// the image build. Renders the Crucible Context box (unchanged) and returns the
// detect rows that join the single Detect domain box.
func (c *crucibleContributor) Detect(rc *domains.RunContext) (domains.Contribution, error) {
	c.req = Request{
		Context: rc.Ctx, RootDir: rc.RootDir, Config: rc.Config, Verbose: rc.Verbose,
		Local: rc.Local, Platforms: rc.Platforms, Target: rc.Target, BuildID: rc.BuildID,
		Stdout: rc.Writer, Stderr: rc.Stderr, Store: rc.Store,
	}
	if c.req.Stderr == nil {
		c.req.Stderr = os.Stderr
	}

	rootDir, err := filepath.Abs(rc.RootDir)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("resolving absolute path: %w", err)
	}
	c.rootDir = rootDir
	c.pipelineStart = time.Now()

	if err := build.EnsureCrucibleAllowed(rootDir); err != nil {
		return domains.Contribution{}, err
	}
	c.runID = build.GenerateCrucibleRunID()
	c.candidateTag = CrucibleTag("candidate", c.runID)
	c.verifyTag = CrucibleTag("verify", c.runID)
	if desc := postbuild.FirstDockerReadmeDescription(rc.Config); desc != "" {
		gitver.SetProjectDescription(desc)
	}
	c.created = time.Unix(c.pipelineStart.Unix(), 0).UTC().Format(time.RFC3339)

	w, color := rc.Writer, rc.Color
	backend, backendErr := ResolveBackendWithConfig(BackendCapabilities{
		Build: true, Run: true, Filesystem: true,
	}, rc.Config.BuildCache.Builder.Backend)

	ctxSec := output.NewSection(w, "Crucible Context", 0, color)
	ctxSec.Row("%-16s%s", "mode", "crucible")
	ctxSec.Row("%-16s%s", "phase", "self-build verification")
	ctxSec.Row("%-16s%s", "epoch", fmt.Sprintf("%d", c.pipelineStart.Unix()))
	ctxSec.Row("%-16s%s", "passes", "2 (candidate → self-proof)")
	ctxSec.Row("%-16s%s", "candidate", c.candidateTag)
	ctxSec.Row("%-16s%s", "verify", c.verifyTag)
	ctxSec.Row("%-16s%s", "platform", fmt.Sprintf("linux/%s", runtime.GOARCH))
	if backendErr == nil {
		ctxSec.Row("%-16s%s", "backend", backend.Kind)
	} else {
		ctxSec.Row("%-16s%s", "backend", "unavailable")
	}
	ctxSec.Close()
	if backendErr != nil {
		return domains.Contribution{}, fmt.Errorf("crucible: no coherent backend: %w", backendErr)
	}
	c.backend = backend

	engine, err := build.Get("image")
	if err != nil {
		return domains.Contribution{}, err
	}
	c.engine = engine
	det, err := engine.Detect(rc.Ctx, rootDir)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("crucible detection: %w", err)
	}
	c.det = det

	var rows []string
	for _, df := range det.Dockerfiles {
		rows = append(rows, fmt.Sprintf("%-9s Dockerfile → %s", "docker", df.Path))
	}
	rows = append(rows, fmt.Sprintf("%-9s %s · crucible (2-pass)", "docker", det.Language))
	return domains.Contribution{
		Rows:    rows,
		Status:  "success",
		Summary: fmt.Sprintf("%d Dockerfile(s), %s", len(det.Dockerfiles), det.Language),
	}, nil
}

// Plan plans the (single-arch) crucible image build and returns the plan rows.
func (c *crucibleContributor) Plan(rc *domains.RunContext) (domains.Contribution, error) {
	planCfg := *rc.Config
	builds := make([]config.BuildConfig, len(planCfg.Builds))
	copy(builds, planCfg.Builds)
	for i := range builds {
		if builds[i].Kind != "docker" {
			continue
		}
		if rc.BuildID != "" && builds[i].ID != rc.BuildID {
			continue
		}
		builds[i].Platforms = []string{fmt.Sprintf("linux/%s", runtime.GOARCH)}
		if rc.Target != "" {
			builds[i].Target = rc.Target
		}
	}
	planCfg.Builds = builds

	plan, err := c.engine.Plan(rc.Ctx, &build.ImagePlanInput{Cfg: &planCfg, BuildID: rc.BuildID}, c.det)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("crucible planning: %w", err)
	}
	c.plan = plan

	row := fmt.Sprintf("%-9s %d build(s) · linux/%s · candidate→verify", "docker", len(plan.Steps), runtime.GOARCH)
	return domains.Contribution{
		Rows:    []string{row},
		Status:  "success",
		Summary: fmt.Sprintf("%d build(s), 2 tag(s)", len(plan.Steps)),
	}, nil
}

// Build runs the trust-critical two-pass self-proof. Builder/Cache/pass boxes
// render exactly as before; the candidate→self-proof sequence is ATOMIC here.
func (c *crucibleContributor) Build(rc *domains.RunContext) (domains.Contribution, error) {
	w, color, ctx := rc.Writer, rc.Color, rc.Ctx

	// ── Builder ──
	c.builderInfo = ResolveBuilderInfo(EnsureBuilderWithBackend(rc.Config.BuildCache.Builder, c.backend))
	RenderBuilderInfo(w, color, c.builderInfo)

	// ── Cache ──
	pc := &pipeline.PipelineContext{Ctx: ctx, RootDir: c.rootDir, Config: rc.Config, Writer: w, Color: color, Verbose: rc.Verbose}
	cacheInfo := ResolveCacheInfo(pc)
	RenderCacheInfo(w, color, cacheInfo)
	if rc.Config.BuildCache.IsActive() {
		versionInfo, _ := build.DetectVersion(c.rootDir, rc.Config)
		cacheRepoID := resolveRepoIDFromContext(pc)
		cacheBranch := ""
		if versionInfo != nil {
			cacheBranch = versionInfo.Branch
		}
		if bch := os.Getenv("CI_COMMIT_BRANCH"); bch != "" && cacheBranch == "" {
			cacheBranch = bch
		}
		if cacheBranch == "" {
			cacheBranch = "default"
		}
		_, c.cacheTo = BuildCacheFlags(rc.Config.BuildCache, cacheRepoID, cacheBranch, rc.Config.Targets, rc.Config.Registries, rc.Config.Vars)
	}

	// ── Pass 1 (candidate) ──
	pass1Plan := clonePlan(c.plan)
	for i := range pass1Plan.Steps {
		pass1Plan.Steps[i].Tags = []string{c.candidateTag}
		pass1Plan.Steps[i].Load = true
		pass1Plan.Steps[i].Push = false
		pass1Plan.Steps[i].Registries = nil
		pass1Plan.Steps[i].CacheTo = nil
	}
	build.InjectLabels(pass1Plan, build.StandardLabels(
		build.NormalizeBuildPlan(pass1Plan), version.Version, version.Commit, "crucible-candidate", c.created))

	_, pass1Err := executeBuildPass(ctx, w, color, rc.Verbose, c.req.Stderr,
		"Build (pass 1: candidate)", pass1Plan, c.candidateTag)
	if pass1Err != nil {
		crucibleVerdict(w, "the calf is not yet mature",
			"Self-build failed; leadership remains with the current tribe leader.")
		row := fmt.Sprintf("%-9s crucible candidate   %s", "docker", output.StatusIcon("failed", color))
		return domains.Contribution{Rows: []string{row}, Status: "failed", Summary: "pass 1 candidate failed"}, pass1Err
	}

	// ── Pass 2 (self-proof) — atomic with pass 1 ──
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    ══════════════════════════════════════════════════════════════")
	fmt.Fprintln(w, "    Pass 2: Crucible — the calf will now self-assess its readiness to lead the tribe")
	fmt.Fprintf(w, "    candidate: %s\n", c.candidateTag)
	fmt.Fprintln(w, "    ══════════════════════════════════════════════════════════════")
	fmt.Fprintln(w)

	pass2Plan := clonePlan(c.plan)
	for i := range pass2Plan.Steps {
		pass2Plan.Steps[i].Tags = []string{c.verifyTag}
		pass2Plan.Steps[i].Load = true
		pass2Plan.Steps[i].Push = false
		pass2Plan.Steps[i].Registries = nil
		pass2Plan.Steps[i].CacheTo = nil
	}
	build.InjectLabels(pass2Plan, build.StandardLabels(
		build.NormalizeBuildPlan(pass2Plan), version.Version, version.Commit, "crucible-verify", c.created))

	_, pass2Err := executeBuildPass(ctx, w, color, rc.Verbose, c.req.Stderr,
		"Rebuild (pass 2: self-proof)", pass2Plan, c.verifyTag)
	c.cruciblePassed = pass2Err == nil

	candIcon := output.StatusIcon("success", color)
	proofIcon := output.StatusIcon("success", color)
	status, summary := "success", "candidate + self-proof"
	if !c.cruciblePassed {
		proofIcon = output.StatusIcon("failed", color)
		status, summary = "failed", "pass 2 self-proof failed"
	}
	rows := []string{
		fmt.Sprintf("%-9s crucible candidate   %s", "docker", candIcon),
		fmt.Sprintf("%-9s crucible self-proof  %s", "docker", proofIcon),
	}
	// On pass-2 failure we still proceed (no error) so Publish renders provenance
	// + verdict and fails the run there — preserving the monolith's ordering.
	return domains.Contribution{Rows: rows, Status: status, Summary: summary}, nil
}

// Verify compares the candidate and self-proof images. Trust-critical: calls
// VerifyCrucible verbatim. Returns the verification rows for the Verify domain.
func (c *crucibleContributor) Verify(rc *domains.RunContext) (domains.Contribution, error) {
	if !c.cruciblePassed {
		return domains.Contribution{Skip: true}, nil
	}
	verification, err := VerifyCrucible(rc.Ctx, c.candidateTag, c.verifyTag)
	if err != nil {
		verification = &CrucibleVerification{TrustLevel: build.TrustViable}
	}
	c.verification = verification

	var rows []string
	for _, ck := range verification.ArtifactChecks {
		rows = append(rows, fmt.Sprintf("%-9s artifact / %-16s %s  %s",
			"docker", ck.Name, checkStatusIcon(ck.Status, rc.Color), ck.Detail))
	}
	for _, ck := range verification.ExecutionChecks {
		rows = append(rows, fmt.Sprintf("%-9s execution / %-15s %s  %s",
			"docker", ck.Name, checkStatusIcon(ck.Status, rc.Color), ck.Detail))
	}
	rows = append(rows, fmt.Sprintf("%-9s trust level: %s", "docker", build.TrustLevelLabel(verification.TrustLevel)))
	return domains.Contribution{
		Rows: rows, Status: "success", Summary: build.TrustLevelLabel(verification.TrustLevel),
	}, nil
}

// Publish builds + retains the verified artifact (gated INSIDE crucible on the
// trust verdict), records into the run's shared manifest (the run writes once),
// then renders retention/provenance/verdict exactly as the monolith did.
func (c *crucibleContributor) Publish(rc *domains.RunContext) (domains.Contribution, error) {
	w, color, ctx := rc.Writer, rc.Color, rc.Ctx
	transport := transportActive(c.req)
	publishPassed := false
	var publishResult *build.BuildResult

	if c.cruciblePassed && (c.verification == nil || !c.verification.HasHardFailure()) {
		publishPlan := clonePlan(c.plan)
		for i := range publishPlan.Steps {
			if transport {
				publishPlan.Steps[i].Load = false
				publishPlan.Steps[i].Push = false
				stage := filepath.Join(c.rootDir, ".stagefreight")
				_ = os.MkdirAll(stage, 0o755)
				if layoutDir, tmpErr := os.MkdirTemp(stage, "oci-layout-*"); tmpErr == nil {
					publishPlan.Steps[i].OCILayoutDir = layoutDir
					defer os.RemoveAll(layoutDir)
				}
			} else {
				publishPlan.Steps[i].Load = false
				publishPlan.Steps[i].Push = true
			}
			if !transport {
				publishPlan.Steps[i].CacheTo = c.cacheTo
			}
			if metaFile, tmpErr := os.CreateTemp("", "crucible-publish-metadata-*.json"); tmpErr == nil {
				publishPlan.Steps[i].MetadataFile = metaFile.Name()
				metaFile.Close()
				defer os.Remove(metaFile.Name())
			}
		}
		build.InjectLabels(publishPlan, build.StandardLabels(
			build.NormalizeBuildPlan(publishPlan), version.Version, version.Commit, "crucible-verified", c.created))

		loginFailed := false
		if !transport {
			loginBx := NewBuildx(false)
			loginBx.Stdout = io.Discard
			loginBx.Stderr = io.Discard
			for _, step := range publishPlan.Steps {
				if hasRemoteRegistries(step.Registries) {
					if loginErr := loginBx.Login(ctx, step.Registries); loginErr != nil {
						loginFailed = true
						sec := output.NewSection(w, "Publish (verified artifact: pass 2)", 0, color)
						sec.Row("%-14s%s", "status", "blocked — registry login failed")
						sec.Row("%-14s%v", "error", loginErr)
						sec.Close()
					}
					break
				}
			}
		}

		if !loginFailed {
			pubResult, publishErr := executeBuildPass(ctx, w, color, rc.Verbose, c.req.Stderr,
				"Publish (verified artifact: pass 2)", publishPlan, "")
			if publishErr == nil {
				outputs, planErr := build.PlanToOutputs(publishPlan, build.PlanToOutputsOpts{
					Commit:   os.Getenv("CI_COMMIT_SHA"),
					Pipeline: &artifact.Pipeline{ID: os.Getenv("CI_PIPELINE_ID"), Provider: "gitlab"},
				})
				if planErr == nil {
					for _, step := range pubResult.Steps {
						artifactID := artifact.NewArtifactID("docker", step.Name)
						for _, obs := range step.Publications {
							rc.RB.Record(artifactID, artifact.Outcome{
								Type: artifact.OutcomeTypePush,
								Target: &artifact.OutcomeTarget{
									Kind: "registry", Host: obs.Host, Path: obs.Path, Tag: obs.Tag,
								},
								Push: &artifact.PushOutcome{
									Status: artifact.OutcomeSuccess, Digest: obs.Digest,
									ObservedDigest: obs.Digest, ObservedBy: "buildx",
								},
							})
						}
					}
					captureArtifactDigests(publishPlan, &outputs)
					persistArtifacts(publishPlan, &outputs, rc.Store, w, color)
					// Merge into the run's single shared manifest — the run finalizes
					// + writes outputs.json/published.json once, so binary and docker
					// artifacts coexist (the former clobber is gone).
					rc.Outputs.Artifacts = append(rc.Outputs.Artifacts, outputs.Artifacts...)
					publishPassed = true
					publishResult = pubResult
				}
			}
		}
	}

	// ── Cache Retention / prune (success only) ──
	if c.cruciblePassed {
		repoID := resolveRepoIDFromContext(&pipeline.PipelineContext{Ctx: ctx, RootDir: c.rootDir, Config: rc.Config, Writer: w, Color: color, Verbose: rc.Verbose})
		if c.backend.IsBuildkit() {
			pruneResult := pruneBuildkitCache(c.builderInfo.Name, rc.Config.BuildCache.Local.Retention, rc.Verbose)
			renderBuildkitPrune(w, color, pruneResult, rc.Verbose)
			if pruneResult.Error != nil {
				fmt.Fprintf(w, "    ⚠ cache prune failed — retention policy not enforced: %v\n", pruneResult.Error)
			}
		} else {
			renderLocalRetention(w, color, enforceLocalRetention(
				LocalCacheDir(repoID, rc.Config.BuildCache.Local), rc.Config.BuildCache.Local.Retention))
		}
		ext := rc.Config.BuildCache.External
		if ext.Target != "" && (ext.Retention.MaxRefs > 0 || ext.Retention.StaleAge != "") {
			renderExternalRetention(w, color, enforceExternalRetention(ctx, ext, repoID, rc.Config.Targets, rc.Config.Registries, rc.Config.Vars))
		}
	}

	// ── Image Retention ──
	if c.cruciblePassed && c.plan != nil && postbuild.HasRetention(c.plan) {
		_, _ = postbuild.RunRetentionSection(ctx, w, output.IsCI(), color, c.plan)
	}

	// ── Provenance ──
	c.writeProvenance(w, color)

	// ── Verdict (unchanged text) ──
	switch {
	case !c.cruciblePassed:
		crucibleVerdict(w, "the calf is not yet mature",
			"Self-build failed; leadership remains with the current tribe leader.")
	case c.verification != nil && c.verification.HasHardFailure():
		crucibleVerdict(w, "self-awareness remains incomplete",
			"The calf's self-assessment differs from the judgment of the tribe leader.")
	default:
		crucibleVerdict(w, "the calf has proven its maturity",
			"This build now leads the tribe.")
	}

	if !c.cruciblePassed {
		return domains.Contribution{
			Rows: []string{fmt.Sprintf("%-9s self-build failed", "docker")}, Status: "failed", Summary: "crucible failed",
		}, fmt.Errorf("crucible: self-build failed")
	}
	if publishPassed && publishResult != nil {
		pushed := 0
		for _, step := range publishResult.Steps {
			if len(step.Publications) > 0 {
				pushed += len(step.Publications)
			} else {
				pushed += len(step.Images)
			}
		}
		detail := fmt.Sprintf("%-9s image retained — distribution deferred to publish phase", "docker")
		if pushed > 0 {
			detail = fmt.Sprintf("%-9s %d image(s) (verified artifact)", "docker", pushed)
		}
		return domains.Contribution{Rows: []string{detail}, Status: "success", Summary: "verified artifact"}, nil
	}
	return domains.Contribution{
		Rows: []string{fmt.Sprintf("%-9s publish blocked", "docker")}, Status: "failed", Summary: "publish failed after verification",
	}, nil
}

// writeProvenance renders the Provenance box (unchanged from the monolith).
func (c *crucibleContributor) writeProvenance(w io.Writer, color bool) {
	trust := "failed"
	reproducible := false
	if c.cruciblePassed && c.verification != nil {
		trust = build.TrustLevelLabel(c.verification.TrustLevel)
		reproducible = c.verification.TrustLevel == build.TrustReproducible
	}
	provPath := filepath.Join(c.rootDir, ".stagefreight", "provenance", fmt.Sprintf("crucible-%s.json", c.runID))
	stmt := build.ProvenanceStatement{
		Type:          "https://in-toto.io/Statement/v1",
		PredicateType: "https://slsa.dev/provenance/v1",
		Subject:       []build.ProvenanceSubject{{Name: c.verifyTag}},
		Predicate: build.ProvenancePredicate{
			BuildType: "https://stagefreight.dev/build/crucible/v1",
			Builder:   build.ProvenanceBuilder{ID: "pkg:docker/stagefreight/crucible"},
			Invocation: build.ProvenanceInvocation{
				Parameters: map[string]any{
					"mode": "crucible", "build_id": c.req.BuildID, "target": c.req.Target,
					"platforms": c.req.Platforms, "local": c.req.Local, "backend": c.backend.Kind,
				},
				Environment: map[string]any{
					"run_id": c.runID, "candidate": c.candidateTag, "verify": c.verifyTag,
				},
			},
			Metadata: build.ProvenanceMetadata{
				BuildStartedOn:  c.pipelineStart.UTC().Format(time.RFC3339),
				BuildFinishedOn: time.Now().UTC().Format(time.RFC3339),
				Completeness:    map[string]bool{"parameters": true, "environment": true, "materials": false},
				Reproducible:    reproducible,
			},
			StageFreight: map[string]any{
				"trust_level": trust, "version": version.Version, "commit": version.Commit,
				"plan_sha256": build.NormalizeBuildPlan(c.plan),
			},
		},
	}
	provSec := output.NewSection(w, "Provenance", 0, color)
	if provErr := build.WriteProvenance(provPath, stmt); provErr == nil {
		provSec.Row("✓  %s", provPath)
	} else {
		provSec.Row("✗  %s", provErr.Error())
	}
	provSec.Close()
}
