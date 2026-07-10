package analysis

import "sort"

// canonicalize groups observations that describe the SAME advisory into one
// Vulnerability each. Two observations are the same vulnerability ONLY when one's
// PRIMARY id (its VulnID) is contained in the other's id-set ({VulnID} ∪
// {Aliases}) — NOT when they merely share a non-primary alias. This is
// deliberately conservative: two DISTINCT advisories that both cross-reference a
// common CVE (A={GHSA-A, CVE-X}, B={GHSA-B, CVE-X}) stay separate, where a bare
// id-set intersection would wrongly collapse them and lose one advisory. Identity
// stays transitive for real alias chains (union-find over the relation), so a
// chain P1→P2→P3 still links. For each component it unions the affected packages
// and aliases, keeps the HIGHEST severity, and picks a deterministic
// representative summary / fixed-in / location. Pure, policy-free, deterministic.
func canonicalize(obs []AdvisoryObservation) []Vulnerability {
	n := len(obs)

	// Union-find over observation indices.
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]] // path halving
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	// Precompute each observation's id-set and primary id.
	idSets := make([]map[string]bool, n)
	primaries := make([]string, n)
	for i, o := range obs {
		set := map[string]bool{}
		for _, id := range observationIDs(o) {
			set[id] = true
		}
		idSets[i] = set
		primaries[i] = o.VulnID
	}

	// Link observations that name the same advisory under the primary-containment
	// relation. O(n²) over the (small) per-file observation set.
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if sameVulnerability(primaries[i], idSets[i], primaries[j], idSets[j]) {
				union(i, j)
			}
		}
	}

	// Bucket observation indices by their component root.
	buckets := map[int][]int{}
	for i := range obs {
		if len(idSets[i]) == 0 {
			continue // no identifier — cannot be a vulnerability
		}
		root := find(i)
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

// sameVulnerability reports whether two observations describe one advisory: one's
// primary id must be an identifier of the other. Sharing only a non-primary alias
// does NOT match (distinct advisories cross-referencing a common CVE stay apart).
func sameVulnerability(primaryA string, idsA map[string]bool, primaryB string, idsB map[string]bool) bool {
	if primaryA != "" && idsB[primaryA] {
		return true
	}
	if primaryB != "" && idsA[primaryB] {
		return true
	}
	return false
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
	pkgVersions := map[string]string{} // package name → representative version ("" if unknown)
	surfaceSet := map[Surface]bool{}   // distinct surfaces this advisory was observed on
	var primaryIDs []string
	var v Vulnerability
	bestRank := -1

	for _, i := range sorted {
		o := obs[i]
		for _, id := range observationIDs(o) {
			idSet[id] = true
		}
		if o.Surface != "" {
			surfaceSet[o.Surface] = true
		}
		if o.VulnID != "" {
			primaryIDs = append(primaryIDs, o.VulnID)
		}
		if o.Package != "" {
			// First observation (in the deterministic sorted order) with a
			// non-empty version wins; osv-api sorts before osv-scanner.
			if cur, seen := pkgVersions[o.Package]; !seen || (cur == "" && o.Version != "") {
				pkgVersions[o.Package] = o.Version
			}
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
		if v.Ecosystem == "" && o.Ecosystem != "" {
			v.Ecosystem = o.Ecosystem
		}
	}

	// Canonical ID: the lexicographically smallest primary id (falls back to the
	// smallest id overall if no observation carried a primary).
	v.ID = pickCanonicalID(primaryIDs, idSet)
	v.Aliases = sortedSetExcept(idSet, v.ID)
	v.Packages = formatPackages(pkgVersions)
	v.Surfaces = sortedSurfaces(surfaceSet)
	return v
}

// sortedSurfaces returns the distinct surfaces in a set, sorted for
// determinism; nil when the set is empty.
func sortedSurfaces(set map[Surface]bool) []Surface {
	if len(set) == 0 {
		return nil
	}
	out := make([]Surface, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// formatPackages renders the affected package set as sorted "name@version"
// entries (bare "name" when the version is unknown), for triage in the message.
func formatPackages(pkgVersions map[string]string) []string {
	if len(pkgVersions) == 0 {
		return nil
	}
	out := make([]string, 0, len(pkgVersions))
	for name, ver := range pkgVersions {
		if ver != "" {
			out = append(out, name+"@"+ver)
		} else {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
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
