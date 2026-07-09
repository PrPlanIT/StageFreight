package build

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// PlanToOutputsOpts carries non-plan inputs needed to fully construct the
// intent manifest. Everything here is either plan-time configuration or
// pipeline metadata — never execution state. GeneratedAt is supplied by the
// caller so PlanToOutputs is fully pure (no wall-clock read).
type PlanToOutputsOpts struct {
	GeneratedAt  string // RFC3339; if empty, Finalize defaults to UTC now
	Commit       string
	Pipeline     *artifact.Pipeline
	BinaryPlans  []BinaryArtifactPlan  // plan-time binary descriptors
	ArchivePlans []ArchiveArtifactPlan // plan-time archive descriptors
}

// BinaryArtifactPlan is the plan-time description of one binary output.
// Path is the intended output location relative to the workspace; SHA256
// and any post-build identity belong in the corresponding Outcome.
//
// Binary artifacts are un-targeted by design (Q2, Phase 4): the build
// artifact IS the truth, and distribution destinations are decided at a
// later layer (release_create), not at build time. There is no Targets
// field on the plan because there are no targets in the intent.
type BinaryArtifactPlan struct {
	Name      string
	OS        string
	Arch      string
	Path      string
	Toolchain string
	Version   string
}

// ArchiveArtifactPlan is the plan-time description of one archive output.
// Same un-targeted invariant as BinaryArtifactPlan; Sources references the
// binary artifacts the archive wraps (sibling-artifact relationship, not
// embedding), and lives on the ArchiveOutcome side rather than the plan.
type ArchiveArtifactPlan struct {
	Name    string
	Format  string
	Path    string
	Version string
}

// PlanToOutputs is a pure function from BuildPlan + plan-time options to
// OutputsManifest. Allocation-only: it must not inspect execution state,
// must not infer runtime outcomes, must not fall back on partial build
// metadata. If a field is not plan-derivable, it does not belong in the
// returned manifest.
//
// The returned manifest is fully finalized — schema version, generated_at,
// artifact ids, sort order, and embedded checksum are all populated. It is
// suitable to either pass directly to WriteOutputsManifest for persistence
// or to ResultsBuilder.Build as the in-memory intent snapshot. No disk
// round-trip is required between the two uses.
func PlanToOutputs(plan *BuildPlan, opts PlanToOutputsOpts) (artifact.OutputsManifest, error) {
	out := artifact.OutputsManifest{
		GeneratedAt: opts.GeneratedAt,
		Commit:      opts.Commit,
		Pipeline:    opts.Pipeline,
	}

	if plan != nil {
		for _, step := range plan.Steps {
			if step.Output != OutputImage {
				continue
			}
			// Produce is decided by builds:, not by whether a publish target
			// matched this ref. Every image step becomes an outputs artifact so it
			// is retained to the content store and review-scannable — even on a ref
			// no target matches. Its Registries (target distribution intent) become
			// the artifact's Targets; a step with no matching target yields an
			// artifact with zero Targets ("produced != published"). Distribution of
			// those bytes is the publish phase's decision, keyed on targets+events.
			var targets []artifact.Target
			if len(step.Registries) > 0 {
				targets = make([]artifact.Target, 0, len(step.Registries))
				for _, reg := range step.Registries {
					tags := make([]string, len(reg.Tags))
					copy(tags, reg.Tags)
					targets = append(targets, artifact.Target{
						Kind: "registry",
						Registry: &artifact.RegistryTarget{
							Host:       reg.URL,
							Path:       reg.Path,
							Tags:       tags,
							NativeScan: reg.NativeScan,
						},
					})
				}
			}
			platforms := make([]string, len(step.Platforms))
			copy(platforms, step.Platforms)
			descriptor := &artifact.DockerDescriptor{
				Dockerfile: step.Dockerfile,
				Context:    step.Context,
				Platforms:  platforms,
			}
			if len(step.BuildArgs) > 0 {
				descriptor.BuildArgsDigest = hashBuildArgs(step.BuildArgs)
			}
			out.Artifacts = append(out.Artifacts, artifact.Artifact{
				Kind:    "docker",
				Name:    step.Name,
				Docker:  descriptor,
				Targets: targets,
			})
		}
	}

	// Binary and archive artifacts are intentionally constructed without
	// Targets. Q2 (Phase 4 design): un-targeted by design. Distribution
	// destinations are decided at release time in a separate subsystem.
	for _, b := range opts.BinaryPlans {
		out.Artifacts = append(out.Artifacts, artifact.Artifact{
			Kind:    "binary",
			Name:    b.Name,
			Version: b.Version,
			Binary: &artifact.BinaryDescriptor{
				OS:        b.OS,
				Arch:      b.Arch,
				Path:      b.Path,
				Toolchain: b.Toolchain,
			},
		})
	}

	for _, a := range opts.ArchivePlans {
		out.Artifacts = append(out.Artifacts, artifact.Artifact{
			Kind:    "archive",
			Name:    a.Name,
			Version: a.Version,
			Archive: &artifact.ArchiveDescriptor{
				Format: a.Format,
				Path:   a.Path,
			},
		})
	}

	// Deterministic ordering across (Kind, Name) — independent of input
	// source ordering. The three input loops above (docker steps, binary
	// plans, archive plans) come from independent sources, so without this
	// sort the checksum would silently depend on upstream stability.
	sort.SliceStable(out.Artifacts, func(i, j int) bool {
		if out.Artifacts[i].Kind != out.Artifacts[j].Kind {
			return out.Artifacts[i].Kind < out.Artifacts[j].Kind
		}
		return out.Artifacts[i].Name < out.Artifacts[j].Name
	})

	if err := out.Finalize(); err != nil {
		return artifact.OutputsManifest{}, err
	}
	return out, nil
}

// hashBuildArgs returns a deterministic content hash of build args.
// Key-sorted k=v lines, SHA-256, "sha256:" prefix. Used in DockerDescriptor
// so the intent records *that* build args participated in the build
// without disclosing the args themselves (which may contain secrets).
func hashBuildArgs(args map[string]string) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{'='})
		h.Write([]byte(args[k]))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
