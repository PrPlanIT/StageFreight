package docker

import (
	"fmt"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
)

func init() {
	domains.RegisterContributor(func() domains.Contributor { return &imageContributor{} })
}

// imageContributor builds plain container images (kind: docker WITHOUT
// build_mode: crucible) and distributes them through the perform domain spine,
// the lifecycle counterpart of the standalone `stagefreight docker build`.
//
// It shares the SAME transport-correct build core as executePhase and the
// crucible contributor — applyImageBuildStrategy (retain-vs-push), executeBuildPass
// (structured, buffered build output), setupTransportPlan (metadata + OCI export),
// captureArtifactDigests, persistArtifactsRecords (content-store retain), and
// recordPublicationOutcomes — rather than duplicating any of it. The three valid
// shapes all build in the lifecycle: binary-only, image-only, binary+image.
// Crucible self-builds (build_mode: crucible) are owned by crucibleContributor;
// this contributor skips them.
//
// State is run-scoped: one instance threads Detect → Plan → Build.
type imageContributor struct {
	engine      build.Engine
	det         *build.Detection
	plan        *build.BuildPlan
	builds      []config.BuildConfig
	builderInfo BuilderInfo
	backend     *Backend
	buildStart  time.Time
}

func (c *imageContributor) Name() string { return "image" }
func (c *imageContributor) Order() int   { return 15 } // between binary(10) and crucible(20)

// NeedsDocker: a plain image build needs Docker, but not crucible-strength
// thresholds (there is no 2-pass self-proof).
func (c *imageContributor) NeedsDocker() bool   { return true }
func (c *imageContributor) NeedsCrucible() bool { return false }

func (c *imageContributor) Applies(rc *domains.RunContext) bool {
	for _, b := range rc.Config.Builds {
		if b.Kind == "docker" && b.BuildMode == "" {
			if rc.BuildID == "" || b.ID == rc.BuildID {
				return true
			}
		}
	}
	return false
}

// plainDockerConfig returns a shallow copy of the run config whose Builds contain
// only plain (non-crucible) docker entries, so the image engine never processes a
// crucible build (those belong to crucibleContributor).
func (c *imageContributor) plainDockerConfig(rc *domains.RunContext) *config.Config {
	cfg := *rc.Config
	var builds []config.BuildConfig
	for _, b := range rc.Config.Builds {
		if b.Kind == "docker" && b.BuildMode == "" {
			builds = append(builds, b)
		}
	}
	cfg.Builds = builds
	return &cfg
}

// Detect resolves the image engine, repo detection, and the plain docker builds.
func (c *imageContributor) Detect(rc *domains.RunContext) (domains.Contribution, error) {
	eng, err := build.Get("image")
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("loading image engine: %w", err)
	}
	c.engine = eng

	det, err := eng.Detect(rc.Ctx, rc.RootDir)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("image detection: %w", err)
	}
	c.det = det

	for _, b := range rc.Config.Builds {
		if b.Kind != "docker" || b.BuildMode != "" {
			continue
		}
		if rc.BuildID != "" && b.ID != rc.BuildID {
			continue
		}
		c.builds = append(c.builds, b)
	}

	dockerfile := "Dockerfile"
	if len(det.Dockerfiles) > 0 {
		dockerfile = det.Dockerfiles[0].Path
	}
	row := fmt.Sprintf("%-9s %d image build(s) · %s", "image", len(c.builds), dockerfile)
	return domains.Contribution{
		Rows:    []string{row},
		Status:  "success",
		Summary: fmt.Sprintf("%d image build(s)", len(c.builds)),
	}, nil
}

// Plan plans the image build(s), resolving the registry targets that match the
// current event (the engine gates targets by branch/tag internally).
func (c *imageContributor) Plan(rc *domains.RunContext) (domains.Contribution, error) {
	input := &build.ImagePlanInput{Cfg: c.plainDockerConfig(rc), BuildID: rc.BuildID}
	plan, err := c.engine.Plan(rc.Ctx, input, c.det)
	if err != nil {
		return domains.Contribution{}, fmt.Errorf("image planning: %w", err)
	}
	c.plan = plan

	platforms := map[string]bool{}
	tags := 0
	for _, s := range plan.Steps {
		for _, p := range s.Platforms {
			platforms[p] = true
		}
		tags += len(s.Tags)
	}
	plist := make([]string, 0, len(platforms))
	for p := range platforms {
		plist = append(plist, p)
	}
	row := fmt.Sprintf("%-9s %d build(s) · %s · %d tag(s)", "image", len(plan.Steps), strings.Join(plist, ", "), tags)
	rows := []string{row}
	// Narrate eligibility skips so a "built but not distributed" outcome explains
	// itself. The reason is the matcher's own — carried on the step, not guessed.
	for _, s := range plan.Steps {
		for _, sk := range s.SkippedTargets {
			rows = append(rows, fmt.Sprintf("%-9s %-28s not distributed — %s", "image", sk.TargetID, sk.Reason))
		}
	}
	return domains.Contribution{
		Rows:    rows,
		Status:  "success",
		Summary: fmt.Sprintf("%d image step(s)", len(plan.Steps)),
	}, nil
}

// Build executes the image build(s) through the shared transport-correct path:
// apply the retain-vs-push strategy, stage transport (metadata + OCI export),
// build with structured/buffered output (executeBuildPass — raw log shown only
// collapsed-on-failure), capture digests, retain to the content store, and record
// any pushed images into the shared run manifest. Under transport the image is
// retained (not pushed) and publish promotes it; otherwise it is pushed here.
func (c *imageContributor) Build(rc *domains.RunContext) (domains.Contribution, error) {
	if c.plan == nil || len(c.plan.Steps) == 0 {
		return domains.Contribution{Skip: true}, nil
	}

	// imageEngine.Plan does not set Push/Load — the strategy lives here, shared
	// with the standalone plan phase so both decide retain-vs-push identically.
	transport := rc.Store != nil && rc.Store.Transport()
	retainViaCAS := rc.Store != nil && rc.Store.RequiresOCIExport()
	applyImageBuildStrategy(c.plan, transport, rc.Local, retainViaCAS)

	// The buildx builder must exist before executeBuildPass (which does not
	// ensure it). The backend resolves the post-build retention path (buildkit
	// prune vs local); a failed resolve leaves it nil and runPostBuildRetention
	// falls back to local retention.
	c.builderInfo = ResolveBuilderInfo(EnsureBuilder(rc.Config.BuildCache.Builder))
	c.backend, _ = ResolveBackendWithConfig(BackendCapabilities{
		Build: true, Run: true, Filesystem: true,
	}, rc.Config.BuildCache.Builder.Backend)

	store := rc.Store
	if store == nil {
		store = cas.NewNoopStore()
	}
	if capErr := cas.AssertStoreCapabilities(store); capErr != nil {
		return domains.Contribution{Status: "failed", Summary: "store capability"}, capErr
	}

	// Login to remote registries for any push step (executeBuildPass does not).
	if err := loginForPushSteps(rc.Ctx, c.plan.Steps); err != nil {
		return domains.Contribution{Status: "failed", Summary: "registry login failed"}, err
	}

	// Shared transport-correct retain core (unconditional OCI-export predicate)
	// + manifest recording, identical to crucible's publish pass.
	c.buildStart = time.Now()
	result, storeRows, pushed, execErr := buildRetainRecord(rc, c.plan, "Image")
	if execErr != nil {
		return domains.Contribution{
			Rows:    []string{fmt.Sprintf("%-9s %s  build failed", "image", output.StatusIcon("failed", rc.Color))},
			Status:  "failed",
			Summary: "image build failed",
		}, execErr
	}

	rows := []string{
		fmt.Sprintf("%-9s builder %s (%s · buildkit %s)", "image", c.builderInfo.Name, c.builderInfo.Driver, c.builderInfo.BuildKit),
	}
	for _, step := range result.Steps {
		state := "built (local)"
		switch {
		case transport:
			state = "retained — distribution deferred to publish phase"
		case len(step.Publications) > 0:
			state = fmt.Sprintf("pushed %d tag(s)", len(step.Publications))
		}
		rows = append(rows, fmt.Sprintf("%-9s %-28s %s  %s", "image", step.Name, output.StatusIcon("success", rc.Color), state))
	}
	rows = append(rows, storeRows...)

	summary := fmt.Sprintf("%d image(s)", len(result.Steps))
	switch {
	case pushed > 0:
		summary += fmt.Sprintf(", %d tag(s) pushed", pushed)
	case transport:
		summary += ", retained for publish"
	}
	return domains.Contribution{Rows: rows, Status: "success", Summary: summary}, nil
}

// Publish runs the plain image's post-build housekeeping — cache/image retention
// and a build-provenance statement — via the shared helpers. The retained bytes
// are distributed later by the publish phase's promoteArtifacts.
func (c *imageContributor) Publish(rc *domains.RunContext) (domains.Contribution, error) {
	if c.plan == nil || len(c.plan.Steps) == 0 {
		return domains.Contribution{Skip: true}, nil
	}

	runPostBuildRetention(rc, c.plan, c.backend, c.builderInfo.Name)

	planSHA := build.NormalizeBuildPlan(c.plan)
	shortID := planSHA
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	subject := c.plan.Steps[0].Name
	if len(c.plan.Steps[0].Tags) > 0 {
		subject = c.plan.Steps[0].Tags[0]
	}
	started := c.buildStart
	if started.IsZero() {
		started = time.Now()
	}
	provRows := writeBuildProvenance(provenanceInput{
		rootDir:   rc.RootDir,
		name:      "docker-" + shortID,
		subject:   subject,
		buildType: "https://stagefreight.dev/build/docker/v1",
		builderID: "pkg:docker/stagefreight/image",
		params: map[string]any{
			"build_id":  rc.BuildID,
			"target":    rc.Target,
			"platforms": rc.Platforms,
			"local":     rc.Local,
		},
		env:          nil,
		started:      started,
		finished:     time.Now(),
		reproducible: false,
		trustLevel:   "",
		planSHA:      planSHA,
	})
	return domains.Contribution{Rows: provRows, Status: "success", Summary: "provenance"}, nil
}
