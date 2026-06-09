package docker

import (
	"fmt"
	"os"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
)

func init() {
	domains.RegisterContributor(func() domains.Contributor { return &imageContributor{} })
}

// imageContributor builds plain container images (kind: docker WITHOUT
// build_mode: crucible) and pushes them to their registry targets, as a domain
// contributor in the perform spine.
//
// It is the lifecycle counterpart of the standalone `stagefreight docker build`
// command: it drives the SAME "image" engine through Detect → Plan → Build so a
// normal application — one that does NOT self-build — gets its image built in
// perform, alongside (or instead of) binary builds. The three valid shapes all
// work: binary-only (binaryContributor), image-only (this), and binary+image
// (both, ordered binary→image). Crucible self-builds (build_mode: crucible) are
// owned by crucibleContributor; this contributor deliberately skips them.
//
// The image engine builds AND pushes in Build (the documented perform-time push),
// so there is no Verify/Publish to add — Build records each published image into
// the shared run manifest (rc.Outputs/rc.RB) the same way crucible does, so the
// run Summary and outputs.json/published.json reflect the images.
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
// crucible build (those belong to crucibleContributor). Non-docker builds are
// dropped too — the image engine ignores them, and dropping keeps the plan clean.
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

// Build executes the image build(s) (build + push to matching registry targets)
// and records each published image into the shared run manifest so the run
// Summary and outputs.json/published.json reflect them.
func (c *imageContributor) Build(rc *domains.RunContext) (domains.Contribution, error) {
	if c.plan == nil || len(c.plan.Steps) == 0 {
		return domains.Contribution{Skip: true}, nil
	}

	result, execErr := c.engine.Execute(rc.Ctx, c.plan)

	var rows []string
	for _, step := range result.Steps {
		state := "built (local)"
		if len(step.Publications) > 0 {
			state = fmt.Sprintf("pushed %d tag(s)", len(step.Publications))
		}
		rows = append(rows, fmt.Sprintf("%-9s %-28s %s  %s",
			"image", step.Name, output.StatusIcon("success", rc.Color), state))
	}
	if execErr != nil {
		rows = append(rows, fmt.Sprintf("%-9s %s  build failed", "image", output.StatusIcon("failed", rc.Color)))
		return domains.Contribution{Rows: rows, Status: "failed", Summary: "image build failed"},
			fmt.Errorf("image build: %w", execErr)
	}

	// Record published images into the shared run manifest — mirrors crucible's
	// publish recording, minus the 2-pass self-proof (the image engine pushes
	// during Build). Push outcomes feed published.json; the artifact descriptors
	// feed outputs.json.
	pushed := 0
	if outputs, planErr := build.PlanToOutputs(c.plan, build.PlanToOutputsOpts{
		Commit:   os.Getenv("CI_COMMIT_SHA"),
		Pipeline: &artifact.Pipeline{ID: os.Getenv("CI_PIPELINE_ID"), Provider: "gitlab"},
	}); planErr == nil {
		for _, step := range result.Steps {
			artifactID := artifact.NewArtifactID("docker", step.Name)
			for _, obs := range step.Publications {
				rc.RB.Record(artifactID, artifact.Outcome{
					Type:   artifact.OutcomeTypePush,
					Target: &artifact.OutcomeTarget{Kind: "registry", Host: obs.Host, Path: obs.Path, Tag: obs.Tag},
					Push: &artifact.PushOutcome{
						Status: artifact.OutcomeSuccess, Digest: obs.Digest,
						ObservedDigest: obs.Digest, ObservedBy: "buildx",
					},
				})
				pushed++
			}
		}
		captureArtifactDigests(c.plan, &outputs)
		rc.Outputs.Artifacts = append(rc.Outputs.Artifacts, outputs.Artifacts...)
	}

	summary := fmt.Sprintf("%d image(s)", len(result.Steps))
	if pushed > 0 {
		summary += fmt.Sprintf(", %d tag(s) pushed", pushed)
	}
	return domains.Contribution{Rows: rows, Status: "success", Summary: summary}, nil
}
