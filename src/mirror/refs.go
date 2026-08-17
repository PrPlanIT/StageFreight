package mirror

import "sort"

// Ref reconciliation is forge-agnostic (plain git push/delete), so — unlike
// releases — a ref cannot carry a provenance marker. The ownership boundary is
// therefore the declared SCOPE: SF mirrors the refs a scope admits, and prunes
// ONLY within that scope. A ref outside the scope (a contributor's branch) is
// foreign and is never created, forced, or deleted.
//
// Two safety invariants fall out:
//   - keep-divergent by default: a ref that exists on both but points elsewhere
//     is NOT force-overwritten unless Force is set (never clobber independent work).
//   - prune requires an explicit scope: with no ownership boundary declared we
//     cannot attribute a mirror-only ref, so prune deletes nothing (safe).

// RefUpdate is one push/delete the plan calls for.
type RefUpdate struct {
	Ref    string // e.g. "refs/heads/main" or a bare branch/tag name (caller's convention)
	SHA    string // target commit (empty for a delete)
	Force  bool   // overwrite a diverged ref (only set when Force is enabled)
	Delete bool   // remove the ref on the mirror
}

// RefOptions tunes ref reconciliation. InScope is the ownership boundary; nil
// means "mirror all" for create/update, but leaves prune a no-op (nothing is
// attributable, so nothing is deleted).
type RefOptions struct {
	Prune bool
	Force bool
	// ForceRef force-updates a SPECIFIC diverged ref even when Force is off — for a
	// ref that is mutable BY DESIGN (a rolling tag alias like "latest" that advances
	// each release and would otherwise perpetually diverge). Immutable refs are left to
	// the default keep-divergent path. nil means "no per-ref force". Force (the blanket
	// flag) still wins for every ref when set.
	ForceRef func(ref string) bool
	// IsAncestor reports whether the first commit is an ancestor of the second. It
	// distinguishes a fast-forward (the mirror ref is behind the source) from a true
	// divergence: a differing ref whose mirror commit is an ancestor of the source
	// commit fast-forwards (a non-force Update), everything else keeps divergent. It
	// must return false whenever either commit is not resolvable in local history — a
	// mirror commit absent locally IS the divergence signal. nil ⇒ every differing ref
	// is treated as diverged (the ancestry-blind behavior).
	IsAncestor func(ancestor, descendant string) bool
	InScope    func(ref string) bool
}

// RefPlan is the computed, side-effect-free reconciliation plan.
type RefPlan struct {
	Create   []RefUpdate // refs missing on the mirror
	Update   []RefUpdate // fast-forward, or forced when diverged+Force
	Diverged []string    // diverged, kept (Force off) — surfaced, never clobbered
	Prune    []RefUpdate // OUR refs (in scope) gone from source — deletes
	Foreign  []string    // mirror-only refs outside scope — left untouched
	InSync   int         // already matching
}

// PlanRefs computes the convergence plan from source and mirror ref sets
// (name → commit SHA). Pure and deterministic; ApplyRefPlan executes it.
func PlanRefs(srcRefs, mirrorRefs map[string]string, opts RefOptions) RefPlan {
	inScope := opts.InScope
	scoped := inScope != nil
	if inScope == nil {
		inScope = func(string) bool { return true }
	}
	plan := RefPlan{}

	// deterministic order
	srcNames := sortedKeys(srcRefs)
	for _, name := range srcNames {
		if !inScope(name) {
			continue // not a ref we mirror
		}
		src := srcRefs[name]
		cur, exists := mirrorRefs[name]
		switch {
		case !exists:
			plan.Create = append(plan.Create, RefUpdate{Ref: name, SHA: src})
		case cur == src:
			plan.InSync++
		case opts.Force || (opts.ForceRef != nil && opts.ForceRef(name)):
			// Force classification wins over fast-forward: an opted-in ref overwrites
			// regardless of ancestry.
			plan.Update = append(plan.Update, RefUpdate{Ref: name, SHA: src, Force: true})
		case opts.IsAncestor != nil && opts.IsAncestor(cur, src):
			// The mirror ref is behind the source (its commit is an ancestor) — a plain
			// fast-forward, not a divergence. A non-force Update; git advances it cleanly.
			plan.Update = append(plan.Update, RefUpdate{Ref: name, SHA: src, Force: false})
		default:
			plan.Diverged = append(plan.Diverged, name) // true divergence — keep-divergent
		}
	}

	if opts.Prune {
		for _, name := range sortedKeys(mirrorRefs) {
			if _, onSrc := srcRefs[name]; onSrc {
				continue
			}
			// A mirror-only ref. Prune only what we can ATTRIBUTE: an explicit
			// scope means "these are mine." No scope ⇒ unattributable ⇒ untouched.
			if !scoped || !inScope(name) {
				plan.Foreign = append(plan.Foreign, name)
				continue
			}
			plan.Prune = append(plan.Prune, RefUpdate{Ref: name, Delete: true})
		}
	}
	return plan
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
