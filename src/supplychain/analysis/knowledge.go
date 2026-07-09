package analysis

import "sort"

// canonicalize groups observations that describe the SAME advisory into one
// Vulnerability each. Two observations share identity if their id-sets
// ({VulnID} ∪ {Aliases}) intersect; identity is transitive (a chain of shared
// ids links a component), which a union-find over id strings resolves. For each
// component it unions the affected packages and aliases, keeps the HIGHEST
// severity, and picks a deterministic representative summary / fixed-in /
// location. Pure, policy-free, and deterministic: the same observations always
// produce the same vulnerabilities in the same order.
func canonicalize(obs []AdvisoryObservation) []Vulnerability {
	// Union-find over identifier strings.
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]] // path halving
			x = parent[x]
		}
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// Register every id and link all ids of a single observation together.
	for _, o := range obs {
		ids := observationIDs(o)
		for _, id := range ids {
			if _, ok := parent[id]; !ok {
				parent[id] = id
			}
		}
		for i := 1; i < len(ids); i++ {
			union(ids[0], ids[i])
		}
	}

	// Bucket observation indices by their component root.
	buckets := map[string][]int{}
	for i, o := range obs {
		ids := observationIDs(o)
		if len(ids) == 0 {
			continue // no identifier — cannot be a vulnerability
		}
		root := find(ids[0])
		buckets[root] = append(buckets[root], i)
	}

	vulns := make([]Vulnerability, 0, len(buckets))
	for _, idxs := range buckets {
		vulns = append(vulns, mergeComponent(obs, idxs))
	}

	// Deterministic order, independent of union-find root identities and map
	// iteration: sort by canonical ID.
	sort.Slice(vulns, func(i, j int) bool { return vulns[i].ID < vulns[j].ID })
	return vulns
}

// observationIDs returns the non-empty identifier set of an observation.
func observationIDs(o AdvisoryObservation) []string {
	ids := make([]string, 0, len(o.Aliases)+1)
	if o.VulnID != "" {
		ids = append(ids, o.VulnID)
	}
	for _, a := range o.Aliases {
		if a != "" {
			ids = append(ids, a)
		}
	}
	return ids
}

// mergeComponent reduces the observations of one advisory into a single
// canonical Vulnerability.
func mergeComponent(obs []AdvisoryObservation, idxs []int) Vulnerability {
	// Representative order: OSV-API before osv-scanner (prefer manifest
	// attribution over lockfile), then by File, Package, VulnID — fully
	// deterministic.
	sorted := append([]int(nil), idxs...)
	sort.Slice(sorted, func(a, b int) bool {
		oa, ob := obs[sorted[a]], obs[sorted[b]]
		if p := sourcePriority(oa.Source) - sourcePriority(ob.Source); p != 0 {
			return p < 0
		}
		if oa.File != ob.File {
			return oa.File < ob.File
		}
		if oa.Package != ob.Package {
			return oa.Package < ob.Package
		}
		return oa.VulnID < ob.VulnID
	})

	idSet := map[string]bool{}
	pkgSet := map[string]bool{}
	var primaryIDs []string
	var v Vulnerability
	bestRank := -1

	for _, i := range sorted {
		o := obs[i]
		for _, id := range observationIDs(o) {
			idSet[id] = true
		}
		if o.VulnID != "" {
			primaryIDs = append(primaryIDs, o.VulnID)
		}
		if o.Package != "" {
			pkgSet[o.Package] = true
		}
		if r := severityRank(o.Severity); r > bestRank {
			bestRank = r
			v.Severity = normalizeLabel(o.Severity)
		}
		if v.Summary == "" && o.Summary != "" {
			v.Summary = o.Summary
		}
		if v.FixedIn == "" && o.FixedIn != "" {
			v.FixedIn = o.FixedIn
		}
		if v.File == "" && o.File != "" {
			v.File = o.File
			v.Line = o.Line
		}
	}

	// Canonical ID: the lexicographically smallest primary id (falls back to the
	// smallest id overall if no observation carried a primary).
	v.ID = pickCanonicalID(primaryIDs, idSet)
	v.Aliases = sortedSetExcept(idSet, v.ID)
	v.Packages = sortedSet(pkgSet)
	return v
}

// sourcePriority ranks sources for representative selection (lower = preferred).
func sourcePriority(source string) int {
	if source == "osv-api" {
		return 0
	}
	return 1
}

// severityRank maps an OSV severity label to a comparable rank (higher = worse).
func severityRank(label string) int {
	switch normalizeLabel(label) {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MODERATE":
		return 2
	case "LOW":
		return 1
	default:
		return 0
	}
}

// pickCanonicalID chooses the canonical advisory id: the smallest primary id, or
// the smallest id overall if none were primary.
func pickCanonicalID(primaries []string, idSet map[string]bool) string {
	best := ""
	for _, id := range primaries {
		if best == "" || id < best {
			best = id
		}
	}
	if best != "" {
		return best
	}
	for id := range idSet {
		if best == "" || id < best {
			best = id
		}
	}
	return best
}

// sortedSet returns the set's members in sorted order.
func sortedSet(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedSetExcept returns the set's members, minus one, in sorted order.
func sortedSetExcept(set map[string]bool, except string) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		if k == except {
			continue
		}
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
