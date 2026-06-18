package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/domains"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/sign"
	"github.com/PrPlanIT/StageFreight/src/sign/autosign"
	"github.com/PrPlanIT/StageFreight/src/sign/cosign"
	"github.com/PrPlanIT/StageFreight/src/version"
)

// Standard build logic shared by the image and crucible contributors — retain
// core, post-build retention, provenance. Crucible adds only its 2-pass self-proof.

// buildRetainRecord builds one pass and records it into the shared run manifest.
// It stages OCI export for every image step — unconditional, so the retained
// artifact is always promotable in publish — then builds, captures the digest,
// retains the layout to the content store, and records pushes.
func buildRetainRecord(rc *domains.RunContext, plan *build.BuildPlan, label string) (*build.BuildResult, []string, int, error) {
	store := rc.Store
	if store == nil {
		store = cas.NewNoopStore()
	}
	cleanup := setupTransportPlan(plan, store, rc.RootDir)
	defer cleanup()

	result, err := executeBuildPass(rc.Ctx, rc.Writer, rc.Color, rc.Verbose, rc.Stderr, label, plan, "")
	if err != nil {
		return nil, nil, 0, err
	}

	var storeRows []string
	pushed := 0
	outputs, planErr := build.PlanToOutputs(plan, build.PlanToOutputsOpts{
		Commit:   os.Getenv("CI_COMMIT_SHA"),
		Pipeline: &artifact.Pipeline{ID: os.Getenv("CI_PIPELINE_ID"), Provider: "gitlab"},
	})
	if planErr == nil {
		captureArtifactDigests(plan, &outputs)
		storeRows = contentStoreRows(persistArtifactsRecords(plan, &outputs, store))
		pushed = recordPublicationOutcomes(rc.RB, result.Steps)
		if serr := signImages(rc, plan, result.Steps); serr != nil {
			return result, storeRows, pushed, serr
		}
		rc.Outputs.Artifacts = append(rc.Outputs.Artifacts, outputs.Artifacts...)
	}
	return result, storeRows, pushed, nil
}

// signImages signs each successfully published image digest under its target's
// resolved signing profile. Unlike blob signing, the `legacy` default DOES sign
// images when COSIGN_KEY resolves — preserving implicit image signing. Best-effort
// and loud: a failure is recorded as a failed attestation outcome and warned, but
// never aborts the build; whether a missing signature blocks a release is Publish's
// policy. Signing belongs to Publish — this is that step within the publish phase.
func signImages(rc *domains.RunContext, plan *build.BuildPlan, steps []build.StepResult) error {
	cfg := rc.Config.SigningSetup
	if !cfg.SigningEnabled() {
		return nil // global kill switch — no signing this run
	}

	// Index each registry endpoint's profile + multi-arch flag by host/path —
	// the join key back from a buildx PushObservation to its lowered target.
	type sigTarget struct {
		profile   *config.ResolvedSigningProfile
		multiArch bool
	}
	// Key by the NORMALIZED host (scheme/case/trailing-slash stripped) so the
	// lookup matches the buildx-observed obs.Host, which is already normalized. A
	// raw-URL key here silently dropped signing for any non-bare registry URL —
	// bypassing even enforce. Both sides go through the same normalizer.
	byEndpoint := map[string]sigTarget{}
	for _, st := range plan.Steps {
		for _, reg := range st.Registries {
			byEndpoint[normalizeRegistryHost(reg.URL)+"/"+reg.Path] = sigTarget{reg.SigningProfile, len(st.Platforms) > 1}
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	pushedAny, attemptedAny := false, false
	for _, step := range steps {
		artifactID := artifact.NewArtifactID("docker", step.Name)
		for _, obs := range step.Publications {
			if obs.Digest == "" {
				continue
			}
			pushedAny = true
			tgt := byEndpoint[normalizeRegistryHost(obs.Host)+"/"+obs.Path]
			signPlan, tier, doSign, serr := autosign.EffectiveSigner(rc.Ctx, cfg, tgt.profile, rc.RootDir, rc.RootDir, rc.Config.Toolchains.Desired, now)
			if serr != nil {
				return serr // FATAL (continuity / state-dir guard)
			}
			if !doSign {
				continue
			}
			attemptedAny = true
			digestRef := obs.Host + "/" + obs.Path + "@" + obs.Digest
			target := &artifact.OutcomeTarget{Kind: "registry", Host: obs.Host, Path: obs.Path, Tag: obs.Tag}
			evidence := artifact.TrustEvidence{
				TrustClass:       string(signPlan.TrustClass),
				Tier:             tier,
				PhysicalPresence: signPlan.RequiresPhysicalPresence,
				NonExportable:    signPlan.RequiresNonExportableKey,
				Transparency:     signPlan.TransparencyRequired,
				SignerRef:        sign.SignerRef(signPlan),
			}
			err := cosign.SignImage(rc.Ctx, rc.RootDir, rc.Config.Toolchains.Desired, digestRef, signPlan, cosign.Env{}, sign.SignOptions{MultiArch: tgt.multiArch})
			if err != nil {
				diag.Warn("image signing %s: %v", digestRef, err)
				rc.RB.Record(artifactID, artifact.Outcome{
					Type: artifact.OutcomeTypeAttestation, Target: target,
					Attestation: &artifact.AttestationOutcome{
						Status: artifact.OutcomeFailed, Kind: "cosign",
						VerifiedDigest: obs.Digest, TrustEvidence: evidence, Error: err.Error(),
					},
				})
				if tgt.profile != nil && tgt.profile.Enforce {
					return fmt.Errorf("signing image %s (enforce): %w", digestRef, err)
				}
				continue
			}
			rc.RB.Record(artifactID, artifact.Outcome{
				Type: artifact.OutcomeTypeAttestation, Target: target,
				Attestation: &artifact.AttestationOutcome{
					Status: artifact.OutcomeSuccess, Kind: "cosign",
					SignatureRef: digestRef, VerifiedDigest: obs.Digest, TrustEvidence: evidence,
				},
			})
		}
	}

	// Visible advisory: signing is enabled and images were published, but NO signer
	// resolved — so the operator does not silently ship unsigned images believing
	// signing is on. (Per-image signing FAILURES are warned separately above.)
	if pushedAny && !attemptedAny {
		diag.Warn("signing is enabled but no images were signed — %s", autosign.InactiveReason(cfg))
	}
	return nil
}

// runPostBuildRetention enforces cache retention (buildkit/local/external) and
// image retention. A nil backend skips the buildkit branch (local retention runs).
func runPostBuildRetention(rc *domains.RunContext, plan *build.BuildPlan, backend *Backend, builderName string) {
	w, color, ctx := rc.Writer, rc.Color, rc.Ctx

	// ── Cache Retention / prune ──
	repoID := resolveRepoIDFromContext(&pipeline.PipelineContext{
		Ctx: ctx, RootDir: rc.RootDir, Config: rc.Config, Writer: w, Color: color, Verbose: rc.Verbose,
	})
	if backend != nil && backend.IsBuildkit() {
		pruneResult := pruneBuildkitCache(builderName, rc.Config.BuildCache.Local.Retention, rc.Verbose)
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

	// ── Image Retention ──
	if plan != nil && postbuild.HasRetention(plan) {
		_, _ = postbuild.RunRetentionSection(ctx, w, output.IsCI(), color, plan)
	}
}

// provenanceInput is the per-caller data for writeBuildProvenance.
type provenanceInput struct {
	rootDir, name, subject, buildType, builderID, trustLevel, planSHA string
	params, env                                                       map[string]any
	started, finished                                                 time.Time
	reproducible                                                      bool
}

// writeBuildProvenance writes .stagefreight/provenance/<name>.json (in-toto/SLSA)
// and returns the ✓/✗ evidence row. trust_level is recorded only when non-empty.
func writeBuildProvenance(in provenanceInput) []string {
	provPath := filepath.Join(in.rootDir, ".stagefreight", "provenance", in.name+".json")

	sf := map[string]any{
		"version":     version.Version,
		"commit":      version.Commit,
		"plan_sha256": in.planSHA,
	}
	if in.trustLevel != "" {
		sf["trust_level"] = in.trustLevel
	}

	stmt := build.ProvenanceStatement{
		Type:          "https://in-toto.io/Statement/v1",
		PredicateType: "https://slsa.dev/provenance/v1",
		Subject:       []build.ProvenanceSubject{{Name: in.subject}},
		Predicate: build.ProvenancePredicate{
			BuildType: in.buildType,
			Builder:   build.ProvenanceBuilder{ID: in.builderID},
			Invocation: build.ProvenanceInvocation{
				Parameters:  in.params,
				Environment: in.env,
			},
			Metadata: build.ProvenanceMetadata{
				BuildStartedOn:  in.started.UTC().Format(time.RFC3339),
				BuildFinishedOn: in.finished.UTC().Format(time.RFC3339),
				Completeness:    map[string]bool{"parameters": true, "environment": true, "materials": false},
				Reproducible:    in.reproducible,
			},
			StageFreight: sf,
		},
	}
	if provErr := build.WriteProvenance(provPath, stmt); provErr != nil {
		return []string{fmt.Sprintf("%-9s provenance ✗ %s", "docker", provErr.Error())}
	}
	return []string{fmt.Sprintf("%-9s provenance ✓ %s", "docker", provPath)}
}

// contentStoreRows folds the content-store retention evidence into Publish rows
// (was the standalone "Content Store (retained — not pushed)" panel).
func contentStoreRows(records []RetainedArtifact) []string {
	var rows []string
	for _, r := range records {
		if r.Failure != "" {
			rows = append(rows, fmt.Sprintf("%-9s content-store ✗ %s (%s)", "docker", r.Name, r.Failure))
			continue
		}
		rows = append(rows, fmt.Sprintf("%-9s retained %s · %s · deferred to publish", "docker", r.Name, r.Digest))
	}
	return rows
}

// loginForPushSteps logs buildx into the remote registries of the first pushing
// step. No-op for retain-only (transport) plans where nothing pushes.
func loginForPushSteps(ctx context.Context, steps []build.BuildStep) error {
	for _, step := range steps {
		if step.Push && hasRemoteRegistries(step.Registries) {
			bx := NewBuildx(false)
			bx.Stdout = io.Discard
			bx.Stderr = io.Discard
			if err := bx.Login(ctx, step.Registries); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
