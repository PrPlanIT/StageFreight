package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/supplychain/discovery"
)

// readPublishedLabels reads the OCI config labels off a live published tag. A package var so
// tests can stub the network; bound to the anonymous canonical-registry reader.
var readPublishedLabels = discovery.ManifestLabelsCanonical

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
	description   string // project-metadata.description → OCI image.description label
	pipelineStart time.Time
	backend       *Backend
	engine        build.Engine
	det           *build.Detection
	plan          *build.BuildPlan
	builderInfo   BuilderInfo
	cacheTo       []build.CacheRef

	buildAttempted bool // crucible reached its self-build (gates the Verdict)
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
	c.description = postbuild.FirstProjectDescription(rc.Config)
	c.created = time.Unix(c.pipelineStart.Unix(), 0).UTC().Format(time.RFC3339)

	// Resolve the backend now; its facts (mode/passes/tags/backend) fold into the
	// Build box as a subsection rather than a standalone Crucible Context panel.
	backend, backendErr := ResolveBackendWithConfig(BackendCapabilities{
		Build: true, Run: true, Filesystem: true,
	}, rc.Config.BuildCache.Builder.Backend)
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
	c.buildAttempted = true // from here the Verdict is crucible's to render

	// Resolve builder + cache — no standalone Builder/Cache panels; their essential
	// facts fold into the Build box (next) as a subsection.
	c.builderInfo = ResolveBuilderInfo(EnsureBuilderWithBackend(rc.Config.BuildCache.Builder, c.backend))
	pc := &pipeline.PipelineContext{Ctx: ctx, RootDir: c.rootDir, Config: rc.Config, Writer: w, Color: color, Verbose: rc.Verbose}
	cacheInfo := ResolveCacheInfo(pc)

	// Provenance stamp for every pass's OCI labels: the version + commit of the SOURCE
	// under build (identical to what feeds the binary's ldflags), NOT the orchestrator's
	// own version.* globals — those name StageFreight itself and lag the source in
	// self-hosted CI. Resolved once so candidate, self-proof, and the shipped artifact
	// all carry the same, correct revision.
	sfVer, sfCommit := build.ResolveImageStamp(c.rootDir, rc.Config)
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
		_, c.cacheTo = BuildCacheFlags(rc.Config.BuildCache, cacheRepoID, cacheBranch, rc.Config.Registries, rc.Config.Vars)
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
	build.InjectLabels(pass1Plan, build.ImageLabels(
		c.imageLabelInput(rc.Config, pass1Plan, "crucible-candidate", sfVer, sfCommit)))

	_, pass1Err := executeBuildPass(ctx, w, color, rc.Verbose, c.req.Stderr,
		"Build (pass 1: candidate)", pass1Plan, c.candidateTag)
	if pass1Err != nil {
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
	build.InjectLabels(pass2Plan, build.ImageLabels(
		c.imageLabelInput(rc.Config, pass2Plan, "crucible-verify", sfVer, sfCommit)))

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
	// Build box, single domain unit: folded context/builder/cache subsection,
	// then the candidate + self-proof result rows. (The pass-1/pass-2 layer boxes
	// — the build log itself — still render above; they are not metadata.)
	rows := []string{
		fmt.Sprintf("%-9s crucible · 2-pass (candidate→verify) · platform linux/%s · backend %s",
			"docker", runtime.GOARCH, c.backend.Kind),
		fmt.Sprintf("%-9s builder %s (%s · buildkit %s)",
			"docker", c.builderInfo.Name, c.builderInfo.Driver, c.builderInfo.BuildKit+builderMemorySuffix(c.builderInfo)),
	}
	if cacheInfo.Mode != "" && cacheInfo.Mode != "off" {
		cacheRow := fmt.Sprintf("%-9s cache %s", "docker", cacheInfo.Mode)
		if cacheInfo.Branch != "" {
			cacheRow += " (branch " + cacheInfo.Branch + ")"
		}
		rows = append(rows, cacheRow)
	}
	rows = append(rows,
		fmt.Sprintf("%-9s crucible candidate   %s", "docker", candIcon),
		fmt.Sprintf("%-9s crucible self-proof  %s", "docker", proofIcon),
	)
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

// imageLabelInput resolves the complete OCI + StageFreight label set for a build pass from the
// repo's own config and detected Dockerfile. Each conventional label is taken from its one
// truthful source — metadata for title/licenses/docs/url, the org identity for vendor/authors,
// the primary coordinate for source, the final-stage FROM for base lineage — and left empty when
// that source is absent, so build.ImageLabels omits it (SF never invents a label).
func (c *crucibleContributor) imageLabelInput(cfg *config.Config, plan *build.BuildPlan, mode, sfVer, sfCommit string) build.ImageLabelInput {
	in := build.ImageLabelInput{
		PlanHash:    build.NormalizeBuildPlan(plan),
		SFVersion:   sfVer,
		Commit:      sfCommit,
		Mode:        mode,
		Created:     c.created,
		Description: c.description,
	}
	if cfg != nil {
		m := cfg.Metadata
		in.Title = m.Title
		in.Licenses = m.License
		in.Documentation = m.DocsURL
		in.URL = m.Website
		in.Source = config.PrimaryURL(cfg)
		for _, org := range cfg.Orgs {
			if org.ID == m.Org {
				in.Authors = org.Maintainer
				in.Vendor = org.ID
				break
			}
		}
		in.Maintenance = maintenanceLabel(cfg.Upstream)
	}
	in.BaseName, in.BaseDigest = baseImageLineage(c.det)
	return in
}

// maintenanceLabel maps the upstream.version posture to the image's maintenance label: a tracked
// line is kept fresh (CVE rebuilds for the life of the line); anything else is a pinned
// point-in-time snapshot, not kept fresh. Empty on a repo that declares no upstream — the posture
// is N/A for a non-fork, so the label is omitted rather than claiming a snapshot that isn't one.
func maintenanceLabel(u config.UpstreamConfig) string {
	if !u.Declared() {
		return ""
	}
	if mode, _ := u.Version.Mode(); mode == config.UpstreamVersionTrack {
		return "tracked"
	}
	return "pinned"
}

// baseImageLineage returns the final-stage FROM reference (minus any digest) and that digest when
// the FROM is digest-pinned — the org.opencontainers.image.base.* pair. Parsing only, no network:
// a FROM without a digest yields an empty digest and the label is dropped. The runtime base is the
// last stage of the first detected Dockerfile; when that stage builds FROM an earlier stage (a
// multi-stage alias, not an external image) there is no external lineage to state, so both are
// dropped. (Per-step base lineage for a multi-image repo is a later refinement — labels inject
// uniformly across a plan's steps.)
func baseImageLineage(det *build.Detection) (name, digest string) {
	if det == nil || len(det.Dockerfiles) == 0 {
		return "", ""
	}
	stages := det.Dockerfiles[0].Stages
	if len(stages) == 0 {
		return "", ""
	}
	aliases := make(map[string]bool, len(stages))
	for _, s := range stages {
		if s.Name != "" {
			aliases[s.Name] = true
		}
	}
	from := stages[len(stages)-1].BaseImage
	if from == "" || aliases[from] {
		return "", ""
	}
	if i := strings.LastIndex(from, "@"); i >= 0 {
		return from[:i], from[i+1:]
	}
	return from, ""
}

// resolveBuildNumber returns org.stagefreight.build.number for the shipped image: a monotonic
// count of CONTENT revisions of this version. It reads the currently-published version tag's
// content identity (plan.sha256 + base.digest) and its build.number; when THIS build's content
// matches, the number is reused (a cosmetic rebuild is the same content revision), and only a
// real content change advances it. A version tag not yet published starts at 1. On a transient
// read failure the number cannot be determined safely, so it is omitted (returned "") and logged
// loudly rather than guessed — build.number is advisory metadata, not the integrity anchor, and a
// guessed value would corrupt the count for every downstream observer.
func (c *crucibleContributor) resolveBuildNumber(ctx context.Context, w io.Writer, plan *build.BuildPlan, planHash, baseDigest, version string) string {
	image, tag := publishedVersionRef(plan, version)
	if image == "" || tag == "" {
		return "" // no registry version tag to count from
	}
	labels, err := readPublishedLabels(ctx, image, tag)
	if err != nil {
		fmt.Fprintf(w, "    %-9s build.number omitted — could not read %s:%s (%v)\n", "docker", image, tag, err)
		return ""
	}
	prior, _ := strconv.Atoi(labels[build.LabelBuildNumber])
	if prior <= 0 {
		return "1" // first published (or first numbered) build of this version
	}
	if labels[build.LabelPlanHash] == planHash && labels[build.LabelBaseDigest] == baseDigest {
		return strconv.Itoa(prior) // same content revision — reuse
	}
	return strconv.Itoa(prior + 1)
}

// publishedVersionRef finds the registry image ref (host/path) and the version tag to read the
// currently-published build from — the first registry target carrying a tag equal to the version
// (with or without a leading v). The version tag is the stable identity, so it names the prior
// build of THIS version (a rolling alias like latest might point at a different version).
// Returns empties when no target carries the version tag.
func publishedVersionRef(plan *build.BuildPlan, version string) (image, tag string) {
	bare := strings.TrimPrefix(version, "v")
	want := map[string]bool{version: true, "v" + bare: true, bare: true}
	for _, step := range plan.Steps {
		for _, rt := range step.Registries {
			host := strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(rt.URL, "https://"), "http://"), "/")
			for _, tg := range rt.Tags {
				if want[tg] {
					return host + "/" + rt.Path, tg
				}
			}
		}
	}
	return "", ""
}

// crucibleStepOutput decides how the verified-artifact build step emits its image.
// --local ALWAYS loads into the local daemon and NEVER pushes — that is the whole point of
// --local, and its omission was the "tag is needed when pushing to registry" bug where a
// local build fell through to the push branch and no image ever landed. Transport mode keeps
// the image in the content store (distribution deferred), so it neither loads nor pushes.
// Otherwise the verified artifact is pushed to its registries during this build.
func crucibleStepOutput(local, transport bool) (load, push bool) {
	switch {
	case local:
		return true, false
	case transport:
		return false, false
	default:
		return false, true
	}
}

// Publish builds + retains the verified artifact (gated INSIDE crucible on the
// trust verdict), records into the run's shared manifest (the run writes once),
// then renders retention/provenance/verdict exactly as the monolith did.
func (c *crucibleContributor) Publish(rc *domains.RunContext) (domains.Contribution, error) {
	w, color, ctx := rc.Writer, rc.Color, rc.Ctx
	transport := transportActive(c.req)
	publishPassed := false
	var publishResult *build.BuildResult
	var storeRows []string // Content Store evidence, folded into the Publish box

	if c.cruciblePassed && (c.verification == nil || !c.verification.HasHardFailure()) {
		publishPlan := clonePlan(c.plan)
		for i := range publishPlan.Steps {
			load, push := crucibleStepOutput(rc.Local, transport)
			publishPlan.Steps[i].Load = load
			publishPlan.Steps[i].Push = push
			if push {
				publishPlan.Steps[i].CacheTo = c.cacheTo
			}
		}
		// Same source-of-truth stamp as the build passes: the shipped artifact's labels
		// name the repo under build, not the orchestrator. Promotion (publish_promote.go)
		// re-tags this image digest-preserved WITHOUT re-stamping, so these are the labels
		// that reach the registry — they must be correct here.
		sfVer, sfCommit := build.ResolveImageStamp(c.rootDir, rc.Config)
		in := c.imageLabelInput(rc.Config, publishPlan, "crucible-verified", sfVer, sfCommit)
			in.BuildNumber = c.resolveBuildNumber(ctx, w, publishPlan, in.PlanHash, in.BaseDigest, sfVer)
			build.InjectLabels(publishPlan, build.ImageLabels(in))

		loginFailed := false
		if !transport && !rc.Local {
			if err := loginForPushSteps(ctx, publishPlan.Steps); err != nil {
				loginFailed = true
				sec := output.NewSection(w, "Publish (verified artifact: pass 2)", 0, color)
				sec.Row("%-14s%s", "status", "blocked — registry login failed")
				sec.Row("%-14s%v", "error", err)
				sec.Close()
			}
		}

		if !loginFailed {
			// Shared transport-correct retain core (unconditional OCI-export
			// predicate) + manifest recording. buildRetainRecord runs
			// setupTransportPlan with the same return-true predicate crucible
			// used inline, so retention/distribution behavior is preserved.
			pubResult, pubStoreRows, _, pErr := buildRetainRecord(rc, publishPlan, "Publish (verified artifact: pass 2)")
			publishPassed = pErr == nil
			publishResult = pubResult
			storeRows = pubStoreRows
		}
	}

	// ── Cache + Image Retention (success only) — shared standard housekeeping ──
	if c.cruciblePassed {
		runPostBuildRetention(rc, c.plan, c.backend, c.builderInfo.Name)
	}

	// ── Provenance (folded into the Publish box, no standalone panel) ──
	// Crucible supplies its trust-level + reproducible facts into the shared
	// provenance writer; the statement shape is identical to the monolith's.
	trust := "failed"
	reproducible := false
	if c.cruciblePassed && c.verification != nil {
		trust = build.TrustLevelLabel(c.verification.TrustLevel)
		reproducible = c.verification.TrustLevel == build.TrustReproducible
	}
	provRows, provPath := writeBuildProvenance(provenanceInput{
		rootDir:   c.rootDir,
		name:      "crucible-" + c.runID,
		subject:   c.verifyTag,
		buildType: "https://stagefreight.dev/build/crucible/v1",
		builderID: "pkg:docker/stagefreight/crucible",
		params: map[string]any{
			"mode": "crucible", "build_id": c.req.BuildID, "target": c.req.Target,
			"platforms": c.req.Platforms, "local": c.req.Local, "backend": c.backend.Kind,
		},
		env: map[string]any{
			"run_id": c.runID, "candidate": c.candidateTag, "verify": c.verifyTag,
		},
		started:      c.pipelineStart,
		finished:     time.Now(),
		reproducible: reproducible,
		trustLevel:   trust,
		planSHA:      build.NormalizeBuildPlan(c.plan),
	})

	// Verdict moved to Conclude() — rendered by the run AFTER the Summary.
	if !c.cruciblePassed {
		rows := append([]string{fmt.Sprintf("%-9s self-build failed", "docker")}, provRows...)
		return domains.Contribution{Rows: rows, Status: "failed", Summary: "crucible failed"},
			fmt.Errorf("crucible: self-build failed")
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
		rows := append([]string{detail}, storeRows...)
		rows = append(rows, provRows...)
		// Attest the just-written provenance to each published image under the same
		// trust tier that signed it. Fail-loud: a configured attestation must not
		// silently degrade, so a fatal error fails the verified-artifact publish.
		if err := attestImages(rc, c.plan, provPath); err != nil {
			return domains.Contribution{Rows: rows, Status: "error", Summary: "verified artifact"}, err
		}
		return domains.Contribution{Rows: rows, Status: "success", Summary: "verified artifact"}, nil
	}
	rows := append([]string{fmt.Sprintf("%-9s publish blocked", "docker")}, provRows...)
	return domains.Contribution{Rows: rows, Status: "failed", Summary: "publish failed after verification"}, nil
}

// Conclude renders the Crucible Verdict — the run's final word, AFTER the
// Summary. The sacred text is unchanged; only its position moved (it was inline
// at the end of Publish). Gated on buildAttempted so a failure that happened
// before crucible ran (e.g. the binary contributor) renders no verdict.
func (c *crucibleContributor) Conclude(rc *domains.RunContext) {
	if !c.buildAttempted {
		return
	}
	switch {
	case !c.cruciblePassed:
		crucibleVerdict(rc.Writer, "the calf is not yet mature",
			"Self-build failed; leadership remains with the current tribe leader.")
	case c.verification != nil && c.verification.HasHardFailure():
		crucibleVerdict(rc.Writer, "self-awareness remains incomplete",
			"The calf's self-assessment differs from the judgment of the tribe leader.")
	default:
		crucibleVerdict(rc.Writer, "the calf has proven its maturity",
			"This build now leads the tribe.")
	}

	// The candidate + verify images are Perform-owned execution intermediates —
	// never published (the verify artifact is re-tagged to its real release tags
	// during Publish, which has already run by Conclude), never retained, never
	// referenced by users. Remove them here: Conclude fires on BOTH the success and
	// build-failure paths, so every exit is covered. Best-effort — a leftover image
	// must never downgrade the verdict. Without this the temp tags accumulated
	// ~644 MB/run on the builder (CleanupCrucibleImages existed but had no caller).
	_ = CleanupCrucibleImages(rc.Ctx, c.candidateTag, c.verifyTag)
}
