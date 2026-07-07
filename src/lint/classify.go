package lint

// Mutability is the mutation-safety classification of a set of lint findings. It answers one
// question — "is it safe for StageFreight to mutate this repository to clear these findings?"
// — a mutation-safety judgment, deliberately independent of any CI/pipeline concept (nothing
// here names a phase, a runner, or a gate). That independence is the tell that it belongs at
// the lint layer and is reusable anywhere a caller must decide whether to touch a tree.
//
// Only BLOCKING findings (Finding.Blocks — the same predicate the CI gate uses) are
// classified; a non-blocking finding neither voids the source nor demands a mutator. A
// blocking finding from a "world" module (freshness/osv — external state a mutator repairs;
// see worldModules) is Remediable: a mutator is expected to clear it. Every other blocking
// finding is Fatal — it voids the source, and StageFreight must not mutate a void tree.
type Mutability struct {
	Fatal      []Finding // blocking, no mutator can clear — abort before mutating
	Remediable []Finding // blocking, a mutator is expected to clear (freshness/osv → deps)
}

// HasFatal reports whether any blocking finding voids the source. When true the caller must
// abort before any mutation.
func (m Mutability) HasFatal() bool { return len(m.Fatal) > 0 }

// HasRemediable reports whether there are blocking findings a mutator is expected to clear.
func (m Mutability) HasRemediable() bool { return len(m.Remediable) > 0 }

// Classify partitions the blocking findings into Fatal vs Remediable. Non-blocking findings
// are ignored — they never gate and never affect mutation safety. The blocking test is
// Finding.Blocks, identical to the CI gate, so classification and gating can never disagree
// about what "blocking" means.
func Classify(findings []Finding) Mutability {
	var m Mutability
	for _, f := range findings {
		if !f.Blocks() {
			continue
		}
		if worldModules[f.Module] {
			m.Remediable = append(m.Remediable, f)
		} else {
			m.Fatal = append(m.Fatal, f)
		}
	}
	return m
}
