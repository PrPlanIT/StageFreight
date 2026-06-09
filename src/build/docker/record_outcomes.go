package docker

import (
	"context"
	"os"
	"time"

	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/diag"
	"github.com/PrPlanIT/StageFreight/src/registry"
)

// recordPublicationOutcomes records one push Outcome per buildx-observed
// publication across the given step results, returning the count recorded.
// Digest is sourced ONLY from the buildx PushObservation (containerimage.digest
// via resolvePublished), ObservedBy "buildx". This is the crucible/image
// recording path — NO attestation and NO live registry re-observation;
// executePhase keeps its own recordPushOutcome + attestation path. Under
// transport, steps are not Push, so Publications is empty and nothing is
// recorded here — distribution is deferred to the publish phase.
func recordPublicationOutcomes(rb *build.ResultsBuilder, steps []build.StepResult) int {
	recorded := 0
	for _, step := range steps {
		artifactID := artifact.NewArtifactID("docker", step.Name)
		for _, obs := range step.Publications {
			rb.Record(artifactID, artifact.Outcome{
				Type: artifact.OutcomeTypePush,
				Target: &artifact.OutcomeTarget{
					Kind: "registry", Host: obs.Host, Path: obs.Path, Tag: obs.Tag,
				},
				Push: &artifact.PushOutcome{
					Status: artifact.OutcomeSuccess, Digest: obs.Digest,
					ObservedDigest: obs.Digest, ObservedBy: "buildx",
				},
			})
			recorded++
		}
	}
	return recorded
}

// recordPushOutcome records exactly one push outcome for a single
// (artifact, target, tag) interaction. Returns the resolved digest for use
// by a subsequent (independent) attestation step.
//
// Status reflects the actual observed result of the push call path supplied
// by the caller — this helper never infers status from caller position
// ("we're in the post block, so success"). That would be a control-flow-
// derived truth leak; status is always an explicit parameter.
//
// On success: observes the published digest via buildx + registry API,
// emits warnings on inconsistency, includes the resolved digest in the
// outcome, and returns it.
//
// On non-success (failed / skipped): records the outcome with the supplied
// error message, performs no registry observation, and returns an empty
// digest.
//
// This helper does NOT decide whether to attest. Attestation is an
// independent execution path that consumes the returned digest — never a
// downstream state inference from this outcome's existence.
func recordPushOutcome(
	ctx context.Context,
	rb *build.ResultsBuilder,
	artifactID artifact.ArtifactID,
	target artifact.OutcomeTarget,
	status artifact.OutcomeStatus,
	capturedDigest string,
	credentials string,
	pushErr string,
) string {
	if status != artifact.OutcomeSuccess {
		rb.Record(artifactID, artifact.Outcome{
			Type:   artifact.OutcomeTypePush,
			Target: &target,
			Push: &artifact.PushOutcome{
				Status: status,
				Error:  pushErr,
			},
		})
		return ""
	}

	ref := target.Host + "/" + target.Path + ":" + target.Tag

	var observedBuildx string
	for i := 0; i < 3; i++ {
		d, rErr := ResolveDigest(ctx, ref)
		if rErr == nil {
			observedBuildx = d
			break
		}
		time.Sleep(time.Second)
	}

	var observedAPI string
	apiDigest, apiErr := registry.CheckManifestDigest(ctx, target.Host, target.Path, target.Tag, nil, credentials)
	if apiErr == nil {
		observedAPI = apiDigest
	}

	if observedBuildx != "" && observedAPI != "" && observedBuildx != observedAPI {
		diag.Warn("registry inconsistency: buildx saw %s, registry API saw %s — possible shadow write", observedBuildx, observedAPI)
	}
	if capturedDigest != "" && observedBuildx != "" && capturedDigest != observedBuildx {
		diag.Warn("registry propagation lag: expected %s, registry served %s", capturedDigest, observedBuildx)
	}

	digest := capturedDigest
	if digest == "" {
		digest = observedBuildx
	}

	rb.Record(artifactID, artifact.Outcome{
		Type:   artifact.OutcomeTypePush,
		Target: &target,
		Push: &artifact.PushOutcome{
			Status:         artifact.OutcomeSuccess,
			Digest:         digest,
			ObservedDigest: observedBuildx,
			ObservedBy:     "buildx",
		},
	})
	return digest
}

// recordAttestationOutcomeIfConfigured signs the digest reference and
// records exactly one attestation outcome — either success with refs
// populated or failure with error. Records NOTHING if signing is not
// configured OR digest is empty: absence in the results manifest means
// "not attempted," never "implicit skip."
//
// dssePath is build-scoped and read-only. DSSE provenance is generated
// once at executePhase entry; this helper only stat-checks the path and
// passes it to CosignAttest. Per-target DSSE regeneration would
// reintroduce loop-order-dependent provenance.
//
// multiArch is step-scoped — the caller computes it once from the owning
// BuildStep's platforms, never per-target. That keeps platform-set
// semantics consistent across all tags within a step.
//
// This function does NOT inspect any prior outcome to decide whether to
// sign. Its only inputs are intent (cosignKey configured?), capability
// (digest known?), and execution context (target/dssePath). That keeps
// attestation a structurally independent execution path from push.
func recordAttestationOutcomeIfConfigured(
	ctx context.Context,
	rb *build.ResultsBuilder,
	artifactID artifact.ArtifactID,
	target artifact.OutcomeTarget,
	digest string,
	multiArch bool,
	rootDir string,
	cosignKey string,
	desiredToolchains map[string]config.ToolPinConfig,
	dssePath string,
	credentials string,
) {
	if cosignKey == "" || digest == "" {
		// Not configured OR no digest — record nothing. Absence means
		// "not attempted," which is the truth in both cases.
		return
	}

	digestRef := target.Host + "/" + target.Path + "@" + digest
	signErr := CosignSign(ctx, rootDir, desiredToolchains, digestRef, cosignKey, multiArch)

	// Attest if the build-scoped DSSE file exists. Read-only access only.
	if _, statErr := os.Stat(dssePath); statErr == nil {
		_ = CosignAttest(ctx, rootDir, desiredToolchains, digestRef, dssePath, cosignKey)
	}

	if signErr != nil {
		rb.Record(artifactID, artifact.Outcome{
			Type:   artifact.OutcomeTypeAttestation,
			Target: &target,
			Attestation: &artifact.AttestationOutcome{
				Status: artifact.OutcomeFailed,
				Kind:   "cosign",
				Error:  signErr.Error(),
			},
		})
		return
	}

	links, _ := registry.DiscoverArtifacts(ctx, target.Host, target.Path, digest, credentials, nil)
	rb.Record(artifactID, artifact.Outcome{
		Type:   artifact.OutcomeTypeAttestation,
		Target: &target,
		Attestation: &artifact.AttestationOutcome{
			Status:         artifact.OutcomeSuccess,
			Kind:           "cosign",
			SignatureRef:   links.Signature,
			AttestationRef: links.Provenance,
			VerifiedDigest: digest,
		},
	})
}
