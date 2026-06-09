package docker

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/artifact"
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
	engine build.Engine
	det    *build.Detection
	plan   *build.BuildPlan
	builds []config.BuildConfig
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
	return domains.Contribution{
		Rows:    []string{row},
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
	applyImageBuildStrategy(c.plan, transport, rc.Local)

	// The buildx builder must exist before executeBuildPass (which does not
	// ensure it).
	builderInfo := ResolveBuilderInfo(EnsureBuilder(rc.Config.BuildCache.Builder))

	store := rc.Store
	if store == nil {
		store = cas.NewNoopStore()
	}
	if capErr := cas.AssertStoreCapabilities(store); capErr != nil {
		return domains.Contribution{Status: "failed", Summary: "store capability"}, capErr
	}

	cleanupTransport := setupTransportPlan(c.plan, store, rc.RootDir, func(s build.BuildStep) bool {
		return s.Push || s.Load || s.OCILayoutDir != ""
	})
	defer cleanupTransport()

	// Login to remote registries for any push step (executeBuildPass does not).
	for _, step := range c.plan.Steps {
		if step.Push && hasRemoteRegistries(step.Registries) {
			loginBx := NewBuildx(false)
			loginBx.Stdout = io.Discard
			loginBx.Stderr = io.Discard
			if err := loginBx.Login(rc.Ctx, step.Registries); err != nil {
				return domains.Contribution{Status: "failed", Summary: "registry login failed"}, err
			}
			break
		}
	}

	result, execErr := executeBuildPass(rc.Ctx, rc.Writer, rc.Color, rc.Verbose, rc.Stderr, "Image", c.plan, "")
	if execErr != nil {
		return domains.Contribution{
			Rows:    []string{fmt.Sprintf("%-9s %s  build failed", "image", output.StatusIcon("failed", rc.Color))},
			Status:  "failed",
			Summary: "image build failed",
		}, execErr
	}

	// Record into the single shared run manifest (the run finalizes + writes
	// outputs.json/published.json once). Mirrors crucible's publish recording.
	outputs, planErr := build.PlanToOutputs(c.plan, build.PlanToOutputsOpts{
		Commit:   os.Getenv("CI_COMMIT_SHA"),
		Pipeline: &artifact.Pipeline{ID: os.Getenv("CI_PIPELINE_ID"), Provider: "gitlab"},
	})
	var storeRows []string
	pushed := 0
	if planErr == nil {
		captureArtifactDigests(c.plan, &outputs)
		storeRows = contentStoreRows(persistArtifactsRecords(c.plan, &outputs, store))
		pushed = recordPublicationOutcomes(rc.RB, result.Steps)
		rc.Outputs.Artifacts = append(rc.Outputs.Artifacts, outputs.Artifacts...)
	}

	rows := []string{
		fmt.Sprintf("%-9s builder %s (%s · buildkit %s)", "image", builderInfo.Name, builderInfo.Driver, builderInfo.BuildKit),
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
