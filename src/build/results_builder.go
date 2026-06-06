package build

import (
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/artifact"
)

// ResultsBuilder accumulates Outcomes during execution and produces a
// ResultsManifest at the end. Single-writer (per-process); not concurrent-
// safe — execution is sequential.
//
// Intentionally minimal API: Record appends, Build finalizes. No dedup, no
// reordering policy, no validation beyond the intent ↔ result join.
// "Anything smarter becomes a hidden policy engine."
//
// Cross-domain coupling is restricted to ArtifactID. Names and kinds are
// resolved at Build time from the OutputsManifest — the builder itself does
// not track them. This keeps ResultsBuilder ignorant of intent structure
// and prevents drift via metadata duplication.
type ResultsBuilder struct {
	perArtifact   map[artifact.ArtifactID][]artifact.Outcome // keyed by typed ArtifactID
	artifactOrder []artifact.ArtifactID                      // first-recorded order, used for deterministic Build()
}

// NewResultsBuilder returns an empty builder. The intent_checksum is bound
// at Build() time from the OutputsManifest, not at construction — this
// ensures results always reference the actual approved intent rather than
// a checksum cached at construction.
func NewResultsBuilder() *ResultsBuilder {
	return &ResultsBuilder{
		perArtifact: map[artifact.ArtifactID][]artifact.Outcome{},
	}
}

// ResultsBuilderFromManifest returns a builder pre-seeded with an existing
// manifest's outcomes, so a later phase EXTENDS recorded history rather than
// replacing it. published.json is cumulative observed truth (build outcomes
// recorded by perform, push outcomes by publish) — a phase that re-records its
// own outcomes into a fresh builder would clobber the earlier facts (e.g. drop
// archive build outcomes that release-asset discovery depends on). Outcomes are
// re-recorded in manifest order; the caller then Records this phase's new
// outcomes on top before Build(). A nil manifest yields an empty builder.
func ResultsBuilderFromManifest(m *artifact.ResultsManifest) *ResultsBuilder {
	b := NewResultsBuilder()
	if m == nil {
		return b
	}
	for _, res := range m.Results {
		for _, o := range res.Outcomes {
			b.Record(res.ArtifactID, o)
		}
	}
	return b
}

// Record appends an outcome for an artifact. Append-only. Caller is
// responsible for calling Record exactly once per (artifact, target) tuple
// with the final outcome already fully populated (digest, attestation,
// requirements_met). No post-Record mutation is supported — the outcome
// must be a complete fact at the moment of recording.
//
// artifactID is typed (artifact.ArtifactID, not bare string) to make the
// "ArtifactID is the only join key" invariant compile-enforced. Callers
// supply IDs obtained from artifact.NewArtifactID or from view/manifest
// reads — never assembled from fields inline.
func (b *ResultsBuilder) Record(artifactID artifact.ArtifactID, o artifact.Outcome) {
	if _, exists := b.perArtifact[artifactID]; !exists {
		b.artifactOrder = append(b.artifactOrder, artifactID)
	}
	b.perArtifact[artifactID] = append(b.perArtifact[artifactID], o)
}

// Build finalizes the results manifest. Deterministic ordering: artifacts
// in first-recorded order; outcomes within each artifact in append order.
//
// Returns an error if any recorded outcome references an ArtifactID absent
// from outputs.Artifacts. This is the only cross-domain validation the
// builder performs — it enforces the join key without owning any other
// invariant about intent structure.
//
// outputs must be the in-memory intent snapshot returned by PlanToOutputs
// (or another already-finalized OutputsManifest). Do NOT round-trip through
// disk to obtain it — the architecture deliberately removed cross-phase
// file reads, and reintroducing one here would couple results finalization
// to I/O ordering.
func (b *ResultsBuilder) Build(outputs *artifact.OutputsManifest) (artifact.ResultsManifest, error) {
	if outputs == nil {
		return artifact.ResultsManifest{}, fmt.Errorf("ResultsBuilder.Build: outputs manifest is nil")
	}
	if outputs.Checksum == "" {
		return artifact.ResultsManifest{}, fmt.Errorf("ResultsBuilder.Build: outputs manifest has no checksum (intent must be written before results are finalized)")
	}

	nameByID := make(map[artifact.ArtifactID]string, len(outputs.Artifacts))
	kindByID := make(map[artifact.ArtifactID]string, len(outputs.Artifacts))
	for _, a := range outputs.Artifacts {
		nameByID[a.ID] = a.Name
		kindByID[a.ID] = a.Kind
	}

	var unknown []artifact.ArtifactID
	for _, id := range b.artifactOrder {
		if _, ok := nameByID[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return artifact.ResultsManifest{}, fmt.Errorf("ResultsBuilder.Build: outcomes recorded against unknown artifact ids %v (not present in outputs manifest)", unknown)
	}

	results := make([]artifact.Result, 0, len(b.artifactOrder))
	for _, id := range b.artifactOrder {
		results = append(results, artifact.Result{
			ArtifactID:   id,
			ArtifactName: nameByID[id],
			Kind:         kindByID[id],
			Outcomes:     b.perArtifact[id],
		})
	}

	return artifact.ResultsManifest{
		IntentChecksum: outputs.Checksum,
		Results:        results,
	}, nil
}
