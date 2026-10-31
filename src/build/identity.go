package build

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Standard OCI label keys emitted by StageFreight on every build.
const (
	LabelCreated     = "org.opencontainers.image.created"
	LabelRevision    = "org.opencontainers.image.revision"
	LabelVersion     = "org.opencontainers.image.version"
	LabelDescription = "org.opencontainers.image.description"
	LabelBuildMode   = "org.stagefreight.build.mode"
	LabelPlanHash    = "org.stagefreight.plan.sha256"

	// Conventional OCI image annotations, stamped from the repo's own identity
	// (metadata / org / primary coordinate) and from the Dockerfile FROM. A serious
	// builder emits the full predefined set — but only from a truthful source: each
	// label is omitted when its source is absent (SF never invents one).
	LabelTitle         = "org.opencontainers.image.title"
	LabelSource        = "org.opencontainers.image.source"
	LabelURL           = "org.opencontainers.image.url"
	LabelDocumentation = "org.opencontainers.image.documentation"
	LabelLicenses      = "org.opencontainers.image.licenses"
	LabelVendor        = "org.opencontainers.image.vendor"
	LabelAuthors       = "org.opencontainers.image.authors"

	// Fork lineage: the exact upstream manifest this image was built FROM. base.name is
	// the FROM ref (minus any digest); base.digest is the pinned digest when the FROM
	// carries one. Together they prove "upstream X + our layer" from the image alone.
	LabelBaseName   = "org.opencontainers.image.base.name"
	LabelBaseDigest = "org.opencontainers.image.base.digest"

	// Maintenance posture: "tracked" (a maintained fork line, kept fresh) or "pinned"
	// (a point-in-time snapshot, not kept fresh). Stamped only on forks; absent on a
	// repo that declares no upstream, where the posture is N/A.
	LabelMaintenance = "org.stagefreight.maintenance"

	// BuildNumber is the monotonic count of CONTENT revisions of this version — the
	// human-visible rebuild signal that the version tag deliberately does not carry. It
	// advances only when the build's content identity (plan.sha256 + base.digest) differs
	// from the currently-published build of the same version, so a cosmetic rebuild keeps
	// the same number. Absent when there is no published version tag to count from.
	LabelBuildNumber = "org.stagefreight.build.number"
)

// WithDescription stamps org.opencontainers.image.description from the project's declared
// description (project-metadata.description) into the label set. StageFreight OWNS this label
// whenever the project declares a description: it is written every build (so it stays current,
// not first-build-only) and — because a build --label overrides a Dockerfile LABEL of the same
// key — it is authoritative over any baked-in value, giving one dependable source. When no
// description is configured, the label is left untouched (a Dockerfile's own value, if any,
// stands) — SF never invents one, so there is no surprise clobber.
func WithDescription(labels map[string]string, description string) map[string]string {
	if d := strings.TrimSpace(description); d != "" {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[LabelDescription] = d
	}
	return labels
}

// StandardLabels returns the set of OCI labels that should be applied to
// every image built by StageFreight, regardless of build mode.
func StandardLabels(planHash, sfVersion, commit, mode, createdRFC3339 string) map[string]string {
	if createdRFC3339 == "" {
		createdRFC3339 = time.Now().UTC().Format(time.RFC3339)
	}
	labels := map[string]string{
		LabelCreated:  createdRFC3339,
		LabelPlanHash: planHash,
	}
	if sfVersion != "" {
		labels[LabelVersion] = sfVersion
	}
	if commit != "" {
		labels[LabelRevision] = commit
	}
	if mode == "" {
		mode = "standard"
	}
	labels[LabelBuildMode] = mode
	return labels
}

// ImageLabelInput carries the fully-resolved label values for one build. Every field is a
// plain string so the build package stays free of a config dependency: the caller — which has
// the *config.Config in scope — resolves each value from its one truthful source (metadata, org
// identity, the primary coordinate, the Dockerfile FROM) and leaves a field empty when that
// source is absent. ImageLabels then omits that label rather than inventing one.
type ImageLabelInput struct {
	// Core — the always-present provenance set (fed to StandardLabels/WithDescription).
	PlanHash    string
	SFVersion   string
	Commit      string
	Mode        string // build mode: crucible-candidate | crucible-verify | crucible-verified | standard
	Created     string
	Description string

	// Conventional OCI annotations — each omitted when empty.
	Title         string
	Source        string
	URL           string
	Documentation string
	Licenses      string
	Vendor        string
	Authors       string

	// Fork lineage — the FROM ref and its pinned digest; each omitted when empty.
	BaseName   string
	BaseDigest string

	// Maintenance posture — "tracked" | "pinned"; omitted when empty (not a fork).
	Maintenance string

	// BuildNumber — the content-revision counter; omitted when empty (no published
	// version tag to count from, or the read to determine it failed).
	BuildNumber string
}

// ImageLabels assembles the complete OCI + StageFreight label set for a build, layering the
// conventional image.* annotations, the fork-lineage pair, and the maintenance posture onto the
// core StandardLabels. It is the single stamping surface — the lockstep evidence test exercises
// it, so every key it can emit is registered in config.StampedLabels. A field left empty is
// omitted: StageFreight never invents a label for a source the repo did not declare.
func ImageLabels(in ImageLabelInput) map[string]string {
	labels := WithDescription(
		StandardLabels(in.PlanHash, in.SFVersion, in.Commit, in.Mode, in.Created),
		in.Description,
	)
	set := func(key, val string) {
		if v := strings.TrimSpace(val); v != "" {
			labels[key] = v
		}
	}
	set(LabelTitle, in.Title)
	set(LabelSource, in.Source)
	set(LabelURL, in.URL)
	set(LabelDocumentation, in.Documentation)
	set(LabelLicenses, in.Licenses)
	set(LabelVendor, in.Vendor)
	set(LabelAuthors, in.Authors)
	set(LabelBaseName, in.BaseName)
	set(LabelBaseDigest, in.BaseDigest)
	set(LabelMaintenance, in.Maintenance)
	set(LabelBuildNumber, in.BuildNumber)
	return labels
}

// InjectLabels merges labels into every step of a plan. Existing labels
// on a step are preserved; new labels do not overwrite.
func InjectLabels(plan *BuildPlan, labels map[string]string) {
	for i := range plan.Steps {
		if plan.Steps[i].Labels == nil {
			plan.Steps[i].Labels = make(map[string]string)
		}
		for k, v := range labels {
			if _, exists := plan.Steps[i].Labels[k]; !exists {
				plan.Steps[i].Labels[k] = v
			}
		}
	}
}

// NormalizeBuildPlan produces a deterministic fingerprint of a BuildPlan,
// excluding ephemeral/runtime-derived fields. Used globally for provenance,
// and by crucible for build graph verification between passes.
//
// Included fields (build-affecting):
//   - BuildStep.Name, Dockerfile, Context, Target (build identity)
//   - BuildStep.Platforms (affects output binary)
//   - BuildStep.BuildArgs (minus BUILD_DATE — ephemeral timestamp)
//
// Excluded fields (ephemeral or derived at runtime):
//   - BuildStep.Tags (output naming, not build-affecting)
//   - BuildStep.Registries (output destinations, not build-affecting)
//   - BuildStep.Output (always "image" for docker)
//   - BuildStep.Load, Push (runtime strategy decisions)
//   - BuildStep.Labels (metadata, not build-affecting)
//   - BuildStep.Extract (artifact mode only)
//   - RegistryTarget.Credentials (auth, not build-affecting)
//   - RegistryTarget.Provider (inferred, not build-affecting)
//   - RegistryTarget.Retention, TagPatterns (post-build operations)
//   - BuildArgs["BUILD_DATE"] (timestamp, always differs between runs)
//   - Map iteration order (all maps sorted by key)
//   - Empty/zero-value fields (omitted, not hashed)
//   - Builder-generated metadata (layer IDs, cache keys, etc.)
func NormalizeBuildPlan(plan *BuildPlan) string {
	h := sha256.New()
	for _, step := range plan.Steps {
		fmt.Fprintf(h, "step:%s\n", step.Name)
		fmt.Fprintf(h, "dockerfile:%s\n", filepath.Clean(step.Dockerfile))
		fmt.Fprintf(h, "context:%s\n", filepath.Clean(step.Context))
		// Content of the build inputs, not just their paths. Without this the
		// fingerprint is source-blind: two commits with different code but the same
		// build shape hash identically, so a stale build can be reused / pass
		// crucible / mislabel provenance and ship undetected. Empty when the plan
		// was assembled without the context on disk — degrade to path-only rather
		// than fabricate a match.
		if step.ContextDigest != "" {
			fmt.Fprintf(h, "contextDigest:%s\n", step.ContextDigest)
		}
		if step.Target != "" {
			fmt.Fprintf(h, "target:%s\n", step.Target)
		}

		platforms := make([]string, len(step.Platforms))
		copy(platforms, step.Platforms)
		sort.Strings(platforms)
		fmt.Fprintf(h, "platforms:%s\n", strings.Join(platforms, ","))

		// Sorted build args, excluding ephemeral keys
		argKeys := make([]string, 0, len(step.BuildArgs))
		for k := range step.BuildArgs {
			if k == "BUILD_DATE" {
				continue
			}
			argKeys = append(argKeys, k)
		}
		sort.Strings(argKeys)
		for _, k := range argKeys {
			fmt.Fprintf(h, "arg:%s=%s\n", k, step.BuildArgs[k])
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// HashBuildContext returns a content hash of the inputs that actually determine
// the built bytes: the Dockerfile plus every file in the build context. Any
// source-byte change yields a different hash — the property NormalizeBuildPlan's
// path-only identity was missing. Best-effort and deterministic: files are hashed
// in sorted order; an unreadable path contributes a stable "unreadable" marker
// (so identity degrades to "unknown content" rather than silently colliding);
// noise dirs that never affect the image (.git, .stagefreight, node_modules,
// vendored git) are skipped so the hash reflects source, not build detritus.
func HashBuildContext(dockerfile, contextDir string) string {
	h := sha256.New()

	// Reuse the package's streaming single-file hasher (fileSHA256) rather than
	// re-implementing file hashing.
	if dh, err := fileSHA256(dockerfile); err == nil {
		fmt.Fprintf(h, "dockerfile:%s\n", dh)
	} else {
		fmt.Fprintf(h, "dockerfile:unreadable:%s\n", filepath.Base(dockerfile))
	}

	var rels []string
	_ = filepath.WalkDir(contextDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != contextDir {
				switch d.Name() {
				case ".git", ".stagefreight", "node_modules":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if rel, rerr := filepath.Rel(contextDir, p); rerr == nil {
			rels = append(rels, rel)
		}
		return nil
	})
	sort.Strings(rels)
	for _, rel := range rels {
		if fh, err := fileSHA256(filepath.Join(contextDir, rel)); err == nil {
			fmt.Fprintf(h, "f:%s:%s\n", rel, fh)
		} else {
			fmt.Fprintf(h, "f:%s:unreadable\n", rel)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// EnvFingerprint returns an informational hash of the build environment.
// Non-authoritative — useful for debugging but never a primary signal.
func EnvFingerprint() string {
	h := sha256.New()
	fmt.Fprintf(h, "os:%s\n", runtime.GOOS)
	fmt.Fprintf(h, "arch:%s\n", runtime.GOARCH)
	fmt.Fprintf(h, "go:%s\n", runtime.Version())
	return fmt.Sprintf("%x", h.Sum(nil))
}

// TruncHash truncates a hash string for display.
func TruncHash(s string) string {
	if len(s) > 20 {
		return s[:20] + "..."
	}
	return s
}
