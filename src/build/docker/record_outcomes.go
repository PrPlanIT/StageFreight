package docker

import (
	"github.com/PrPlanIT/StageFreight/src/artifact"
	"github.com/PrPlanIT/StageFreight/src/build"
)

// recordPublicationOutcomes records one push Outcome per buildx-observed
// publication across the given step results, returning the count recorded.
// Digest is sourced ONLY from the buildx PushObservation (containerimage.digest
// via resolvePublished), ObservedBy "buildx". This is the crucible/image
// recording path — NO attestation and NO live registry re-observation. Under
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
