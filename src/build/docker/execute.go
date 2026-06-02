package docker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/build/pipeline"
	"github.com/PrPlanIT/StageFreight/src/cas"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/output"
	"github.com/PrPlanIT/StageFreight/src/postbuild"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// extractExitCode extracts the process exit code from an error.
// Returns 1 if the error is not an exec.ExitError.
func extractExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

// transportActive reports whether cross-phase artifact transport is active for
// this request. It keys on the store's POLICY capability (Transport), not on the
// mechanism (RequiresOCIExport): when transport is active, perform retains the
// built bytes and does NOT distribute — publish promotes the retained bytes — so
// external distribution occurs only during publish. A nil store (non-lifecycle
// callers) is no transport.
func transportActive(req Request) bool {
	return req.Store != nil && req.Store.Transport()
}

// captureArtifactDigests reads the OCI index digest from each image step's
// buildx metadata file and writes it onto the matching artifact in the frozen
// outputs manifest, keyed by step name. This is where artifact identity is
// materialized at build completion, independent of publication.
//
// Identity is sourced ONLY from containerimage.digest (the OCI index digest).
// It is never read from docker inspect {{.Id}}, which is a per-platform image
// config ID that coincides with the index digest only for trivial
// single-platform images and diverges on multi-platform builds.
//
// Best-effort: a step whose metadata is absent or unparseable simply leaves
// Digest empty. WriteOutputsManifest re-finalizes, so any captured digest is
// folded into the manifest checksum.
func captureArtifactDigests(plan *build.BuildPlan, outputs *artifact.OutputsManifest) {
	if plan == nil || outputs == nil {
		return
	}
	digestByName := make(map[string]artifact.Digest)
	for _, step := range plan.Steps {
		if step.Output != build.OutputImage || step.MetadataFile == "" {
			continue
		}
		if d, err := ParseMetadataDigest(step.MetadataFile); err == nil && d != "" {
			digestByName[step.Name] = artifact.Digest(d)
		}
	}
	for i := range outputs.Artifacts {
		if outputs.Artifacts[i].Kind != "docker" {
			continue
		}
		if d, ok := digestByName[outputs.Artifacts[i].Name]; ok {
			outputs.Artifacts[i].Digest = d
		}
	}
}

// persistArtifacts retains each docker artifact's exact OCI layout bytes in the
// content store and records the persistence handle on the matching artifact.
// This is the transport floor: build once, carry the bytes forward, prove
// identity by content digest.
//
// It runs only for stores that required OCI export (layouts were emitted);
// NoopStore exports nothing, so the step/layout map is empty and every handle
// stays at its zero value. cas.Put verifies the layout matches the digest
// before retaining, so a layout/digest mismatch fails loudly here rather than
// surfacing as a phantom artifact later.
//
// The handle is written but NOT consumed by any decision in this phase — review
// and publish do not read it yet. Presence of a handle must never be read as
// implicit trust; it becomes load-bearing only when a later phase resolves and
// re-hashes through it.
func persistArtifacts(plan *build.BuildPlan, outputs *artifact.OutputsManifest, store cas.Store, w io.Writer, color bool) {
	if plan == nil || outputs == nil || store == nil || !store.RequiresOCIExport() {
		return
	}
	layoutByName := make(map[string]string)
	for _, step := range plan.Steps {
		if step.Output == build.OutputImage && step.OCILayoutDir != "" {
			layoutByName[step.Name] = step.OCILayoutDir
		}
	}
	for i := range outputs.Artifacts {
		a := &outputs.Artifacts[i]
		if a.Kind != "docker" || a.Digest == "" {
			continue
		}
		layoutDir, ok := layoutByName[a.Name]
		if !ok {
			continue
		}
		storedDir, err := store.Put(cas.Digest(a.Digest), layoutDir)
		if err != nil {
			// Persistence failure is loud but non-fatal to the build: the image
			// was built and (if push/load) is available; what's lost is the
			// transport guarantee for this artifact. Surface it so it is never
			// silently assumed present downstream.
			fmt.Fprintf(w, "    %s persistence failed for %s: %v\n",
				output.StatusIcon("warn", color), a.Name, err)
			continue
		}
		a.Persistence = artifact.PersistenceHandle{
			Kind:      artifact.PersistenceOCILayout,
			OCILayout: &artifact.OCILayoutRef{Path: storedDir},
		}
	}
}

// newPushFailure converts a PushTags error into a runtime-agnostic PushFailure.
// This is the single boundary where Docker-specific PushError is consumed.
func newPushFailure(err error, fallbackStderr string) postbuild.PushFailure {
	var pushErr *PushError
	if errors.As(err, &pushErr) {
		return postbuild.PushFailure{
			Err:      err,
			ExitCode: pushErr.ExitCode,
			Stderr:   pushErr.Stderr,
			Tag:      pushErr.Tag,
		}
	}
	return postbuild.PushFailure{
		Err:      err,
		ExitCode: 1,
		Stderr:   fallbackStderr,
	}
}

// isCacheExportError returns true if the build failure is caused by cache export
// (auth, network, permission) rather than the actual build. Cache export failures
// should never break builds — the build is retried without --cache-to.
func isCacheExportError(err error, combinedOutput string) bool {
	lower := strings.ToLower(combinedOutput)
	return strings.Contains(lower, "exporting cache") ||
		strings.Contains(lower, "failed to export cache") ||
		strings.Contains(lower, "error writing layer blob") ||
		strings.Contains(lower, "insufficient_scope")
}

// collectPushRegistries returns the non-local registries from load-then-push
// steps (step.Load && !step.Push). Used to pass registry targets to push
// recovery without inlining the loop at each call site.
func collectPushRegistries(plan *build.BuildPlan) []build.RegistryTarget {
	var regs []build.RegistryTarget
	for _, step := range plan.Steps {
		if !step.Load || step.Push {
			continue
		}
		for _, reg := range step.Registries {
			if reg.Provider != "local" {
				regs = append(regs, reg)
			}
		}
	}
	return regs
}

// executePhase builds images via buildx, pushes, and emits push/attestation
// events. Build + push + sign share buildx state; signing is per-target,
// inline with the push, never post-hoc. Outcomes flow into the supplied
// ResultsBuilder; the OutputsManifest is constructed once from the plan
// and written by the supplied pointer for publishPhase to consume.
func executePhase(req Request, outputsOut *artifact.OutputsManifest, rb *build.ResultsBuilder) pipeline.Phase {
	return pipeline.Phase{
		Name: "build",
		Run: func(pc *pipeline.PipelineContext) (*pipeline.PhaseResult, error) {
			plan := pc.BuildPlan
			if plan == nil {
				return nil, fmt.Errorf("missing build plan")
			}

			// OutputsManifest is constructed once from the resolved plan and
			// frozen for the duration of execution. No re-derivation later.
			builtOutputs, err := build.PlanToOutputs(plan, build.PlanToOutputsOpts{
				Commit:   os.Getenv("CI_COMMIT_SHA"),
				Pipeline: &artifact.Pipeline{ID: os.Getenv("CI_PIPELINE_ID"), Provider: "gitlab"},
			})
			if err != nil {
				return nil, fmt.Errorf("constructing outputs manifest: %w", err)
			}
			*outputsOut = builtOutputs

			output.SectionStart(pc.Writer, "sf_build", "Build")
			buildStart := time.Now()

			// Ensure builder exists (engine owns full lifecycle: context, builder, bootstrap).
			// Then inspect for structured narration.
			builderInfo := EnsureBuilder(pc.Config.BuildCache.Builder)
			builderInfo = ResolveBuilderInfo(builderInfo)
			RenderBuilderInfo(pc.Writer, pc.Color, builderInfo)
			pc.Scratch["docker.builderInfo"] = builderInfo

			// Render cache resolution info (resolve in cache.go, render here).
			cacheInfo := ResolveCacheInfo(pc)
			RenderCacheInfo(pc.Writer, pc.Color, cacheInfo)

			// Always capture output for structured display; verbose forwards stderr in real-time.
			// Capture BOTH stdout and stderr — docker buildx writes compile errors to stdout
			// (progress stream) while docker-level errors go to stderr.
			bx := NewBuildx(pc.Verbose)
			var stderrBuf, stdoutBuf bytes.Buffer
			bx.Stdout = &stdoutBuf
			if pc.Verbose {
				bx.Stderr = req.Stderr
			} else {
				bx.Stderr = &stderrBuf
			}

			// Login to remote registries
			for _, step := range plan.Steps {
				if hasRemoteRegistries(step.Registries) {
					loginBx := *bx
					loginBx.Stdout = io.Discard
					loginBx.Stderr = io.Discard
					if err := loginBx.Login(pc.Ctx, step.Registries); err != nil {
						output.SectionEnd(pc.Writer, "sf_build")
						return nil, err
					}
					break
				}
			}

			// Set up metadata files for digest capture on every image step —
			// push AND load. The OCI index digest (containerimage.digest) is
			// materialized at build completion regardless of output mode, and
			// artifact identity must exist independently of publication. Push
			// steps additionally use this file for the publish-outcome digest.
			var metadataCleanup []string
			for i := range plan.Steps {
				if plan.Steps[i].Output != build.OutputImage {
					continue
				}
				if !plan.Steps[i].Push && !plan.Steps[i].Load {
					continue
				}
				metaFile, tmpErr := os.CreateTemp("", "buildx-metadata-*.json")
				if tmpErr == nil {
					plan.Steps[i].MetadataFile = metaFile.Name()
					metaFile.Close()
					metadataCleanup = append(metadataCleanup, metaFile.Name())
				}
			}
			defer func() {
				for _, f := range metadataCleanup {
					os.Remove(f)
				}
			}()

			// Set up OCI layout export dirs when the content store requires it.
			// The store is asked via its capability (RequiresOCIExport), never by
			// concrete type, so perform pays the export cost only for a store that
			// will retain the bytes. NoopStore returns false → no export, no
			// dormant layout produced and discarded. One temp layout dir per
			// image step; cas.Put copies it into the store after a successful build.
			store := req.Store
			if store == nil {
				store = cas.NewNoopStore()
			}
			// Enforce the store capability invariant at the point a store enters
			// the build — the forbidden quadrant (export demanded, no transport)
			// must fail loudly here, not silently waste work. This is the single
			// real call site that makes AssertStoreCapabilities enforcement rather
			// than documentation.
			if capErr := cas.AssertStoreCapabilities(store); capErr != nil {
				return nil, capErr
			}
			var ociLayoutCleanup []string
			if store.RequiresOCIExport() {
				for i := range plan.Steps {
					if plan.Steps[i].Output != build.OutputImage {
						continue
					}
					if !plan.Steps[i].Push && !plan.Steps[i].Load {
						continue
					}
					layoutDir, tmpErr := os.MkdirTemp("", "sf-oci-layout-*")
					if tmpErr == nil {
						plan.Steps[i].OCILayoutDir = layoutDir
						ociLayoutCleanup = append(ociLayoutCleanup, layoutDir)
					}
				}
			}
			defer func() {
				for _, d := range ociLayoutCleanup {
					os.RemoveAll(d)
				}
			}()

			// Build each step
			var result build.BuildResult
			for _, step := range plan.Steps {
				stderrBuf.Reset()
				stdoutBuf.Reset()
				stepResult, layers, err := bx.BuildWithLayers(pc.Ctx, step)
				if stepResult == nil {
					stepResult = &build.StepResult{Name: step.Name, Status: "failed"}
				}
				stepResult.Layers = layers

				// Registry push recovery: if a multi-platform --push build fails
				// due to a recoverable registry error, attempt vendor recovery and retry once.
				if err != nil && step.Push {
					failure := postbuild.PushFailure{
						Err:      err,
						ExitCode: extractExitCode(err),
						Stderr:   stdoutBuf.String() + "\n" + stderrBuf.String(),
					}
					recovery := postbuild.RecoverPushFailure(pc.Ctx, step.Registries, failure)
					if recovery.Retry {
						diag.Info(recovery.Message)
						stderrBuf.Reset()
						stdoutBuf.Reset()
						stepResult, layers, err = bx.BuildWithLayers(pc.Ctx, step)
						if stepResult == nil {
							stepResult = &build.StepResult{Name: step.Name, Status: "failed"}
						}
						stepResult.Layers = layers
					}
				}

				// Cache export fallback: if build fails due to cache export (auth, network),
				// retry without --cache-to. Cache export must never break builds.
				if err != nil && len(step.CacheTo) > 0 && isCacheExportError(err, stdoutBuf.String()+"\n"+stderrBuf.String()) {
					diag.Warn("cache export failed — retrying build without cache export")
					retryStep := step
					retryStep.CacheTo = nil
					stderrBuf.Reset()
					stdoutBuf.Reset()
					stepResult, layers, err = bx.BuildWithLayers(pc.Ctx, retryStep)
					if stepResult == nil {
						stepResult = &build.StepResult{Name: step.Name, Status: "failed"}
					}
					stepResult.Layers = layers
				}

				result.Steps = append(result.Steps, *stepResult)
				if err != nil {
					buildElapsed := time.Since(buildStart)
					failSec := output.NewSection(pc.Writer, "Build", buildElapsed, pc.Color)
					renderBuildLayers(failSec, result.Steps, pc.Color)
					output.RowStatus(failSec, "status", "build failed", "failed", pc.Color)

					// Semantic error extraction — shared contract via errsurface.go.
					// Combine stdout + stderr: docker buildx writes compile errors
					// to stdout (progress stream), docker-level errors to stderr.
					combinedOutput := stdoutBuf.String() + "\n" + stderrBuf.String()
					RenderBuildError(failSec, combinedOutput)

					failSec.Close()

					if pc.CI {
						output.SectionStartCollapsed(pc.Writer, "sf_build_raw", "Build Output (raw)")
						fmt.Fprint(pc.Writer, combinedOutput)
						output.SectionEnd(pc.Writer, "sf_build_raw")
					} else if pc.Verbose {
						fmt.Fprint(req.Stderr, combinedOutput)
					}

					output.SectionEnd(pc.Writer, "sf_build")
					return &pipeline.PhaseResult{
						Name:    "build",
						Status:  "failed",
						Summary: "build failed",
						Elapsed: buildElapsed,
						Failure: &pipeline.FailureDetail{
							Command:  fmt.Sprintf("docker buildx build %s", step.Name),
							ExitCode: 1,
							Reason:   "build failed",
							Stderr:   stdoutBuf.String() + "\n" + stderrBuf.String(),
						},
					}, err
				}
			}
			buildElapsed := time.Since(buildStart)

			// Capture artifact identity (OCI index digest) from buildx metadata
			// and patch it into the frozen outputs manifest, keyed by step name.
			// Identity is materialized at build completion — independent of
			// publication. Sourced ONLY from containerimage.digest, never from
			// docker inspect {{.Id}} (a per-platform config ID that diverges
			// from the index digest on multi-platform builds). WriteOutputsManifest
			// re-finalizes, so the digest is folded into the manifest checksum.
			captureArtifactDigests(plan, outputsOut)

			// Retain the exact build bytes in the content store (transport floor)
			// and record the persistence handle on each artifact. Only runs when
			// the store required OCI export (so layouts exist); NoopStore is a
			// no-op and leaves handles empty. The handle is written but consumed
			// by NOTHING yet — review and publish do not read it in this phase.
			persistArtifacts(plan, outputsOut, store, pc.Writer, pc.Color)

			// Post-push hooks (scan triggers, etc.) after multi-platform push
			for _, step := range plan.Steps {
				if step.Push {
					postbuild.PostPushHooks(pc.Ctx, step.Registries)
				}
			}

			// Signing setup — build-scoped, computed once. cosignKey is the
			// only signal the attestation helper consumes; empty disables.
			// Collapse availability + key resolution into the single string:
			// no key OR cosign not on PATH = signing disabled.
			cosignKey := ResolveCosignKey()
			if !CosignAvailable(pc.RootDir, pc.Config.Toolchains.Desired) {
				cosignKey = ""
			}

			// DSSE provenance is build-scoped: generated once at this point
			// (from provenance.json if buildx wrote one). Per-target
			// attestation only stat-checks and reads this path — never
			// regenerates. Regenerating per-target would couple provenance
			// to loop order.
			dssePath := filepath.Join(pc.RootDir, ".stagefreight", "provenance.dsse.json")
			if cosignKey != "" {
				if _, statErr := os.Stat(filepath.Join(pc.RootDir, ".stagefreight", "provenance.json")); statErr == nil {
					provenanceData, readErr := os.ReadFile(filepath.Join(pc.RootDir, ".stagefreight", "provenance.json"))
					if readErr == nil {
						var stmt build.ProvenanceStatement
						if jsonErr := json.Unmarshal(provenanceData, &stmt); jsonErr == nil {
							_ = build.WriteDSSEProvenance(dssePath, stmt)
						}
					}
				}
			}

			// Record multi-platform pushes (step.Push = true → buildx --push).
			// SITE 1 v2: per-target push + attestation events via ResultsBuilder.
			// No publishManifest append, no inline cosign call — both moved
			// into recordPushOutcome / recordAttestationOutcomeIfConfigured.
			for _, step := range plan.Steps {
				if !step.Push {
					continue
				}

				var capturedDigest string
				if step.MetadataFile != "" {
					for attempt := 0; attempt < 3; attempt++ {
						if d, mErr := ParseMetadataDigest(step.MetadataFile); mErr == nil {
							capturedDigest = d
							break
						} else if attempt == 2 {
							diag.Warn("could not parse buildx metadata digest: %v", mErr)
						}
						time.Sleep(200 * time.Millisecond)
					}
				}

				artifactID := artifact.NewArtifactID("docker", step.Name)
				multiArch := len(step.Platforms) > 1 // step-scoped

				for _, reg := range step.Registries {
					if reg.Provider == "local" {
						continue
					}
					host := registry.NormalizeHost(reg.URL)

					for _, tag := range reg.Tags {
						target := artifact.OutcomeTarget{
							Kind: "registry",
							Host: host,
							Path: reg.Path,
							Tag:  tag,
						}
						digest := recordPushOutcome(
							pc.Ctx, rb, artifactID, target,
							artifact.OutcomeSuccess,
							capturedDigest, reg.Credentials, "",
						)
						recordAttestationOutcomeIfConfigured(
							pc.Ctx, rb, artifactID, target, digest,
							multiArch, pc.RootDir, cosignKey,
							pc.Config.Toolchains.Desired, dssePath, reg.Credentials,
						)
					}
				}
			}

			// Build section output
			buildSec := output.NewSection(pc.Writer, "Build", buildElapsed, pc.Color)
			if renderBuildLayers(buildSec, result.Steps, pc.Color) {
				buildSec.Separator()
			}

			var buildImageCount int
			for _, sr := range result.Steps {
				for _, img := range sr.Images {
					buildSec.Row("result  %-40s", img)
					buildImageCount++
				}
			}
			buildSec.Close()
			output.SectionEnd(pc.Writer, "sf_build")

			// --- Push (single-platform load-then-push) ---
			// Suppressed under active transport: perform must not distribute.
			// collectRemoteTags returns tags for Load && !Push steps, which is
			// exactly the state transport sets for single-platform builds — so
			// without this guard perform would still push them. Distribution is
			// the publish phase's sole authority; publish promotes the retained
			// layout instead.
			var remoteTags []string
			if !transportActive(req) {
				remoteTags = collectRemoteTags(plan)
			}
			var pushSummary string
			if len(remoteTags) > 0 {
				output.SectionStart(pc.Writer, "sf_push", "Push")
				pushStart := time.Now()

				pushBx := *bx
				pushBx.Stdout = io.Discard
				var pushStderrBuf bytes.Buffer
				if pc.Verbose {
					pushBx.Stderr = io.MultiWriter(req.Stderr, &pushStderrBuf)
				} else {
					pushBx.Stderr = &pushStderrBuf
				}
				pushed, err := pushBx.PushTags(pc.Ctx, remoteTags)
				if err != nil {
					pushRegs := collectPushRegistries(plan)

					failure := newPushFailure(err, pushStderrBuf.String())

					recovery := postbuild.RecoverPushFailure(pc.Ctx, pushRegs, failure)
					if recovery.Retry {
						if recovery.Message != "" {
							diag.Info(recovery.Message)
						}
						// Retry only from the failed tag onward — prior tags already succeeded.
						remaining := remoteTags
						if pushed >= 0 && pushed < len(remoteTags) {
							remaining = remoteTags[pushed:]
						}
						pushStderrBuf.Reset()
						if pc.Verbose {
							pushBx.Stderr = io.MultiWriter(req.Stderr, &pushStderrBuf)
						} else {
							pushBx.Stderr = &pushStderrBuf
						}
						_, err = pushBx.PushTags(pc.Ctx, remaining)
					}
					if err != nil {
						// Re-convert: err may be from retry attempt
						failure = newPushFailure(err, pushStderrBuf.String())
						reason := postbuild.ClassifyPushFailure(failure)

						failedTag := failure.Tag
						if failedTag == "" && len(remoteTags) > 0 {
							failedTag = remoteTags[0]
						}

						detailStderr := failure.Stderr
						if detailStderr == "" || !strings.Contains(detailStderr, "\n") {
							detailStderr = err.Error() + "\n" + detailStderr
						}

						output.SectionEnd(pc.Writer, "sf_push")
						return &pipeline.PhaseResult{
							Name:    "build",
							Status:  "failed",
							Summary: fmt.Sprintf("image push failed — %s", reason),
							Failure: &pipeline.FailureDetail{
								Command:  fmt.Sprintf("docker push %s", failedTag),
								ExitCode: failure.ExitCode,
								Reason:   reason,
								Stderr:   strings.TrimSpace(detailStderr),
							},
						}, err
					}
				}

				// Post-push hooks (scan triggers, etc.) after single-platform push
				for _, step := range plan.Steps {
					if step.Load && !step.Push {
						postbuild.PostPushHooks(pc.Ctx, step.Registries)
					}
				}

				pushElapsed := time.Since(pushStart)
				pushSec := output.NewSection(pc.Writer, "Push", pushElapsed, pc.Color)
				for _, tag := range remoteTags {
					pushSec.Row("%s  %s", output.StatusIcon("success", pc.Color), tag)
				}
				pushSec.Close()

				regSet := make(map[string]bool)
				for _, tag := range remoteTags {
					parts := strings.SplitN(tag, "/", 2)
					if len(parts) > 0 {
						regSet[parts[0]] = true
					}
				}
				pushSummary = fmt.Sprintf("%d tag(s) → %d registry", len(remoteTags), len(regSet))
				output.SectionEnd(pc.Writer, "sf_push")

				// Record single-platform pushes (step.Load && !step.Push).
				// SITE 2 v2: per-target push + attestation events via ResultsBuilder.
				// SITE 3 (cosign post-hoc loop) is gone — attestation now inline,
				// per-target, with no shared lifecycle buffer.
				for _, step := range plan.Steps {
					if !step.Load || step.Push {
						continue
					}
					artifactID := artifact.NewArtifactID("docker", step.Name)
					multiArch := len(step.Platforms) > 1 // step-scoped

					for _, reg := range step.Registries {
						if reg.Provider == "local" {
							continue
						}
						host := registry.NormalizeHost(reg.URL)

						for _, tag := range reg.Tags {
							ref := host + "/" + reg.Path + ":" + tag

							// Single-platform digest resolution: 6-retry with backoff,
							// then local RepoDigests fallback. PushTags doesn't return
							// digests directly, so this is the SITE-2-specific path
							// to a pre-resolved digest before handing off to the helper.
							var capturedDigest string
							for i := 0; i < 6; i++ {
								d, rErr := ResolveDigest(pc.Ctx, ref)
								if rErr == nil {
									capturedDigest = d
									break
								}
								if i == 5 {
									diag.Warn("could not resolve digest for %s via registry after push: %v", ref, rErr)
								}
								time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
							}
							if capturedDigest == "" {
								if d, lErr := ResolveLocalDigest(pc.Ctx, ref); lErr == nil {
									capturedDigest = d
									diag.Info("publish: resolved digest via local RepoDigests fallback for %s", ref)
								}
							}
							if capturedDigest == "" {
								diag.Warn("published %s with no immutable digest — security will fall back to tag-based scanning", ref)
							}

							target := artifact.OutcomeTarget{
								Kind: "registry",
								Host: host,
								Path: reg.Path,
								Tag:  tag,
							}
							digest := recordPushOutcome(
								pc.Ctx, rb, artifactID, target,
								artifact.OutcomeSuccess,
								capturedDigest, reg.Credentials, "",
							)
							recordAttestationOutcomeIfConfigured(
								pc.Ctx, rb, artifactID, target, digest,
								multiArch, pc.RootDir, cosignKey,
								pc.Config.Toolchains.Desired, dssePath, reg.Credentials,
							)
						}
					}
				}
			}

			// publishPhase consumes outputs and rb directly via closure capture
			// — no Scratch handoff. The OutputsManifest is already populated
			// via the outputsOut pointer; rb has accumulated outcomes per
			// (artifact, target) interaction. publishPhase will write both v2
			// manifests and render image refs from the same data.

			buildSummary := fmt.Sprintf("%d image(s)", buildImageCount)
			if pushSummary != "" {
				buildSummary += ", " + pushSummary
			}

			return &pipeline.PhaseResult{
				Name:    "build",
				Status:  "success",
				Summary: buildSummary,
				Elapsed: buildElapsed,
			}, nil
		},
	}
}
