package commit

// CommitRing classifies a commit failure by its relationship to the commit
// path's safety and determinism. The ring determines whether a failure is
// absolute or can be bypassed with explicit maintainer intent.
//
// Three rings, ordered by overrideability:
//
//	RingMechanical   → git integrity is broken. No escape.
//	RingDeterminism  → StageFreight cannot reliably describe what will happen. No escape.
//	RingGovernance   → StageFreight policy or model is degraded, but the commit
//	                   path is still deterministic. Bypassed by --maintainer-override.
//
// The rule: do not let Ring 3 failures disable a still-deterministic commit path.
type CommitRing string

const (
	// RingMechanical covers failures where the git repository, index, or
	// filesystem state makes committing impossible regardless of intent:
	//   - detached HEAD
	//   - unresolved rebase/merge conflict
	//   - index write failure
	//   - hook rejected the commit
	//   - push explicitly requested but mechanically blocked
	// Cannot be overridden.
	RingMechanical CommitRing = "mechanical"

	// RingDeterminism covers failures where StageFreight cannot reliably
	// determine what will be committed, construct the message, or truthfully
	// describe the result:
	//   - missing commit summary
	//   - unresolvable path set
	//   - sync plan logically impossible (not a git error, a planning error)
	// Cannot be overridden.
	RingDeterminism CommitRing = "determinism"

	// RingGovernance covers failures where StageFreight policy, schema, or
	// model is degraded but the commit path itself remains deterministic:
	//   - partial config-model migration incomplete
	//   - generated outputs stale (docs, badges, manifests)
	//   - convention strictness failures
	//   - unrelated StageFreight subsystem broken
	// Can be bypassed with --maintainer-override.
	// Bypasses are recorded in Result.OverriddenBlocks and printed to output.
	RingGovernance CommitRing = "governance"
)

// CommitBlock is a classified failure at any stage of commit planning or execution.
// Hard rings (Mechanical, Determinism) propagate as errors.
// Governance rings accumulate and are either reported or bypassed.
type CommitBlock struct {
	Ring    CommitRing
	ID      string // stable identifier for programmatic use and output
	Message string // human-readable explanation
}

// Overrideable returns true when the block can be bypassed with --maintainer-override.
func (b CommitBlock) Overrideable() bool {
	return b.Ring == RingGovernance
}

// CommitBlocks is an ordered collection of CommitBlock values.
type CommitBlocks []CommitBlock

// HasHard returns true when any non-overrideable block is present.
// Use HasMechanical and HasDeterminism for UX-distinct messaging.
func (bs CommitBlocks) HasHard() bool {
	for _, b := range bs {
		if !b.Overrideable() {
			return true
		}
	}
	return false
}

// HasMechanical returns true when any RingMechanical block is present.
// UX message: "fix your repository state".
func (bs CommitBlocks) HasMechanical() bool {
	for _, b := range bs {
		if b.Ring == RingMechanical {
			return true
		}
	}
	return false
}

// HasDeterminism returns true when any RingDeterminism block is present.
// UX message: "StageFreight cannot safely describe what will be committed".
func (bs CommitBlocks) HasDeterminism() bool {
	for _, b := range bs {
		if b.Ring == RingDeterminism {
			return true
		}
	}
	return false
}

// HasGovernance returns true when any overrideable governance block is present.
func (bs CommitBlocks) HasGovernance() bool {
	for _, b := range bs {
		if b.Overrideable() {
			return true
		}
	}
	return false
}

// GovernanceOnly returns a slice containing only governance-ring blocks.
func (bs CommitBlocks) GovernanceOnly() CommitBlocks {
	var out CommitBlocks
	for _, b := range bs {
		if b.Overrideable() {
			out = append(out, b)
		}
	}
	return out
}
