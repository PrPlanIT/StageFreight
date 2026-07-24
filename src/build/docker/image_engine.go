package docker

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

func init() {
	build.Register("image", func() build.Engine { return &imageEngine{} })
}

// imageEngine builds container images and pushes to registries.
type imageEngine struct{}

func (e *imageEngine) Name() string { return "image" }

func (e *imageEngine) Detect(ctx context.Context, rootDir string) (*build.Detection, error) {
	return build.DetectRepo(rootDir)
}

func (e *imageEngine) Plan(ctx context.Context, cfgRaw interface{}, det *build.Detection) (*build.BuildPlan, error) {
	input, ok := cfgRaw.(*build.ImagePlanInput)
	if !ok {
		return nil, fmt.Errorf("image engine: expected *build.ImagePlanInput, got %T", cfgRaw)
	}
	cfg := input.Cfg

	plan := &build.BuildPlan{}

	// Resolve version for templates (tags, paths, URLs)
	versionInfo, _ := build.DetectVersion(det.RootDir, cfg)
	if versionInfo == nil {
		// No repo, or no tag lineage yet (new project): keep the real commit SHA +
		// branch from the CI env so dev-{sha:8} resolves instead of "dev-unknown".
		versionInfo = gitver.SyntheticVersion()
	}

	// Resolve current branch and tag for target filtering
	currentBranch := resolveBranch(det, versionInfo)
	currentGitTag := os.Getenv("CI_COMMIT_TAG")

	// Filter builds to kind: docker, optionally by --build ID
	var dockerBuilds []config.BuildConfig
	for _, b := range cfg.Builds {
		if b.Kind != "docker" {
			continue
		}
		if input.BuildID != "" && b.ID != input.BuildID {
			continue
		}
		dockerBuilds = append(dockerBuilds, b)
	}

	if len(dockerBuilds) == 0 {
		if input.BuildID != "" {
			return nil, fmt.Errorf("no docker build found with id %q", input.BuildID)
		}
		return nil, fmt.Errorf("no docker builds defined")
	}

	// One BuildStep per docker build entry
	for _, b := range dockerBuilds {
		step, err := planDockerBuild(b, cfg, det, versionInfo, currentBranch, currentGitTag)
		if err != nil {
			return nil, fmt.Errorf("build %q: %w", b.ID, err)
		}
		plan.Steps = append(plan.Steps, *step)
	}

	// Inject build cache flags from config.
	// CacheFrom (import) goes on all steps.
	// CacheTo (export) only on steps that push — auth is only available there.
	// Crucible/load-only steps get import but not export.
	if cfg.BuildCache.IsActive() {
		repoID := resolveRepoID(det, versionInfo)
		branch := currentBranch
		if branch == "" {
			branch = "default"
		}
		cacheFrom, cacheTo := BuildCacheFlags(cfg.BuildCache, repoID, branch, cfg.Targets, cfg.Registries, cfg.Vars)
		for i := range plan.Steps {
			plan.Steps[i].CacheFrom = cacheFrom
			if plan.Steps[i].Push {
				plan.Steps[i].CacheTo = cacheTo
			}
		}
	}

	return plan, nil
}

// planDockerBuild creates a BuildStep for a single docker build entry,
// resolving its registry targets from cfg.Targets.
func planDockerBuild(b config.BuildConfig, cfg *config.Config, det *build.Detection, versionInfo *gitver.VersionInfo, currentBranch, currentGitTag string) (*build.BuildStep, error) {
	// Resolve Dockerfile path
	dockerfile := b.Dockerfile
	if dockerfile == "" && len(det.Dockerfiles) > 0 {
		dockerfile = det.Dockerfiles[0].Path
	}
	if dockerfile == "" {
		return nil, fmt.Errorf("no Dockerfile found")
	}

	// Resolve context
	buildContext := b.Context
	if buildContext == "" {
		buildContext = "."
	}

	// Resolve platforms
	platforms := b.Platforms
	if len(platforms) == 0 {
		platforms = []string{fmt.Sprintf("linux/%s", runtime.GOARCH)}
	}

	// Collect registry targets that reference this build
	var tags []string
	var registries []build.RegistryTarget
	var skipped []build.TargetSkip

	// CRITICAL:
	// tag_sources as map is ONLY for when.git_tags lookup on target conditions.
	// DO NOT reuse this for version selection — that would reintroduce
	// global filtering and break the search-path invariant.
	tagPatternMap := make(map[string]string, len(cfg.Git.Tags))
	for _, ts := range cfg.Git.Tags {
		tagPatternMap[ts.ID] = ts.Pattern
	}

	for i, t := range cfg.Targets {
		if t.Kind != "registry" || t.Build != b.ID {
			continue
		}

		// Eligibility via the single canonical matcher (events, then git_tags,
		// then branches). Docker does not interpret when: itself. A skip carries
		// the matcher's own reason for narration — never re-derived here.
		if elig := config.TargetEligibility(t, config.CIEvent(), currentBranch, currentGitTag, config.CIProvider(), tagPatternMap, cfg.Git.Branches); !elig.Eligible {
			skipped = append(skipped, build.TargetSkip{TargetID: t.ID, Reason: elig.Reason})
			continue
		}

		// Resolve registry identity from identity graph or legacy inline fields.
		resolved, err := config.ResolveRegistryForTarget(t, cfg.Registries, cfg.Vars)
		if err != nil {
			return nil, fmt.Errorf("target[%d] %q: %w", i, t.ID, err)
		}

		resolvedURL := build.ResolveTemplate(resolved.URL, versionInfo)
		resolvedPath := build.ResolveTemplate(resolved.Path, versionInfo)

		tagTemplates := make([]string, len(t.Tags))
		for j, tmpl := range t.Tags {
			tagTemplates[j] = gitver.ResolveVars(tmpl, cfg.Vars)
		}
		resolvedTags := build.ResolveTags(tagTemplates, versionInfo)

		// Resolve provider: from registry/target, or auto-detect from URL.
		provider := resolved.Provider
		if provider == "" {
			provider = build.DetectProvider(resolvedURL)
		}

		// Validate resolved tags conform to OCI spec.
		for _, tag := range resolvedTags {
			if err := registry.ValidateTag(tag); err != nil {
				return nil, fmt.Errorf("target[%d] %q (%s/%s): resolved tag: %w", i, t.ID, resolvedURL, resolvedPath, err)
			}
		}

		// Map retention (pointer to value).
		var retention config.RetentionPolicy
		if t.Retention != nil {
			retention = *t.Retention
		}

		// Resolve the trust profile this target signs published images under
		// (the `legacy` default when unset). Ref validity is already gated at
		// audition; a set-but-unknown ref here is a hard config error.
		signProfile, err := config.ResolveSigningProfileForTarget(t, cfg.SigningSetup.Profiles)
		if err != nil {
			return nil, fmt.Errorf("target[%d] %q: %w", i, t.ID, err)
		}

		target := build.RegistryTarget{
			URL:            resolvedURL,
			Path:           resolvedPath,
			Tags:           resolvedTags,
			Credentials:    resolved.Credentials,
			Provider:       provider,
			Retention:      retention,
			TagPatterns:    t.Tags,
			NativeScan:     t.NativeScan,
			SigningProfile: signProfile,
		}
		registries = append(registries, target)

		for _, tag := range resolvedTags {
			var ref string
			if provider == "local" {
				ref = fmt.Sprintf("%s:%s", resolvedPath, tag)
			} else {
				ref = fmt.Sprintf("%s/%s:%s", resolvedURL, resolvedPath, tag)
			}
			tags = append(tags, ref)
		}
	}

	// Auto-inject standard build args
	buildArgs := b.BuildArgs
	if buildArgs == nil {
		buildArgs = map[string]string{}
	}
	// Resolve vars in build args
	for k, v := range buildArgs {
		buildArgs[k] = gitver.ResolveVars(v, cfg.Vars)
	}
	buildArgs = autoInjectBuildArgs(buildArgs, det, versionInfo, dockerfile)

	step := &build.BuildStep{
		Name:       b.ID,
		Dockerfile: dockerfile,
		Context:    buildContext,
		// Fold the source content into the step identity so NormalizeBuildPlan can
		// tell a code change from an identical build shape (the stale-binary bug).
		ContextDigest:  build.HashBuildContext(dockerfile, buildContext),
		Target:         b.Target,
		Platforms:      platforms,
		BuildArgs:      buildArgs,
		Tags:           tags,
		Output:         build.OutputImage,
		Registries:     registries,
		SkippedTargets: skipped,
	}

	return step, nil
}

// resolveBranch determines the current branch.
// Priority cascade:
//  1. Git detection (branch from local .git).
//  2. CI-provided branch (SF_CI_BRANCH, then CI_COMMIT_BRANCH, GITHUB_REF_NAME).
//  3. Version-info branch — only if it is a real branch.
//
// The CI vars come BEFORE the version-info branch: when git is not on PATH in the
// build env (common in CI build containers), DetectVersion substitutes a synthetic
// VersionInfo{Branch:"unknown"}. If that "unknown" were allowed to win, branch-
// gated targets (when:{branches:[main]}) would never match and NO registry tags
// would resolve — the image would build but never publish. So a real CI branch
// (SF_CI_BRANCH is what the perform job exports) takes precedence, and the
// version-info branch is honored only when it is neither "HEAD" nor "unknown".
func resolveBranch(det *build.Detection, v *build.VersionInfo) string {
	// 1. Git detection — most reliable when available.
	if det.GitInfo != nil && det.GitInfo.Branch != "" {
		return det.GitInfo.Branch
	}
	// 2. CI-provided branch — authoritative in detached-HEAD / git-less CI.
	for _, env := range []string{"SF_CI_BRANCH", "CI_COMMIT_BRANCH", "GITHUB_REF_NAME"} {
		if b := os.Getenv(env); b != "" {
			return b
		}
	}
	// 3. Version-info branch — only a real branch, never synthetic "unknown"/"HEAD".
	if v != nil && v.Branch != "" && v.Branch != "HEAD" && v.Branch != "unknown" {
		return v.Branch
	}
	return ""
}

// resolveRepoID returns a stable repo identity for cache scoping.
// Uses CI project path, git remote origin, or directory name as fallback.
func resolveRepoID(det *build.Detection, v *build.VersionInfo) string {
	// CI project path (most reliable in CI).
	if p := os.Getenv("CI_PROJECT_PATH"); p != "" {
		return p
	}
	// Git remote URL.
	if det.GitInfo != nil && det.GitInfo.Remote != "" {
		return det.GitInfo.Remote
	}
	// Fallback to root dir basename.
	if det.RootDir != "" {
		return det.RootDir
	}
	return "unknown"
}

// autoInjectBuildArgs adds VERSION, COMMIT, and BUILD_DATE build args when the
// Dockerfile declares matching ARGs and no explicit override is set.
func autoInjectBuildArgs(existing map[string]string, det *build.Detection, v *build.VersionInfo, dockerfilePath string) map[string]string {
	if v == nil {
		return existing
	}

	// Find the parsed Dockerfile info that matches our chosen Dockerfile
	var dfArgs []string
	for _, df := range det.Dockerfiles {
		if df.Path == dockerfilePath {
			dfArgs = df.Args
			break
		}
	}
	if len(dfArgs) == 0 {
		return existing
	}

	// Build a set of Dockerfile ARG names for fast lookup
	argSet := make(map[string]bool, len(dfArgs))
	for _, a := range dfArgs {
		argSet[a] = true
	}

	// Inject if Dockerfile declares the ARG and no explicit override exists
	if argSet["VERSION"] {
		if _, ok := existing["VERSION"]; !ok {
			existing["VERSION"] = v.Version
		}
	}
	if argSet["COMMIT"] {
		if _, ok := existing["COMMIT"]; !ok {
			existing["COMMIT"] = v.SHA
		}
	}
	if argSet["BUILD_DATE"] {
		if _, ok := existing["BUILD_DATE"]; !ok {
			if pinned := os.Getenv("STAGEFREIGHT_BUILD_DATE"); pinned != "" {
				existing["BUILD_DATE"] = pinned
			} else {
				existing["BUILD_DATE"] = time.Now().UTC().Format(time.RFC3339)
			}
		}
	}

	return existing
}
