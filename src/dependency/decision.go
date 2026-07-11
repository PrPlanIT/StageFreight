package dependency

// SkipCategory is the typed classification of WHY a dependency was not updated,
// set at the point the decision is made rather than reverse-engineered from prose
// afterwards. It is the single source of truth for the decision; the human-readable
// SkippedDep.Reason is the presentation of it.
//
// This is the explanation spine: today grouping and disclosure still render the
// free-form Reason string (so output is unchanged), but every skip now carries an
// intrinsic category that downstream consumers — uniform disclosure, remediation
// evaluation, candidate-set provenance — can read WITHOUT string-matching. Adding a
// new skip site means naming its category here, not teaching a parser a new phrase.
type SkipCategory string

const (
	// SkipNone is the zero value: the dependency is a candidate, not skipped.
	SkipNone SkipCategory = ""

	// --- policy / freshness decisions (filter.go) ---
	SkipUpToDate          SkipCategory = "up_to_date"           // already at the compatible target
	SkipMajorHeld         SkipCategory = "major_held"           // constraint-expanding major out of range, review-only
	SkipCeilingExceeded   SkipCategory = "ceiling_exceeded"     // beyond max_update, no in-ceiling re-target
	SkipSecurityOnly      SkipCategory = "security_only_policy"  // non-vulnerable dep under policy=security
	SkipEcosystemFiltered SkipCategory = "ecosystem_filtered"   // excluded by the ecosystem filter
	SkipNotAutoUpdatable  SkipCategory = "not_auto_updatable"   // ecosystem cannot be auto-updated
	SkipIndirect          SkipCategory = "indirect"             // transitively managed, not updated directly
	SkipUnresolved        SkipCategory = "unresolved"           // latest could not be verified
	SkipFileUntracked     SkipCategory = "file_untracked"       // owning file not tracked by git
	SkipDockerConstraint  SkipCategory = "docker_constraint"    // digest-pin / ARG / untagged / latest tag

	// --- apply-layer decisions (apply_*.go) ---
	SkipSourceUnresolvable SkipCategory = "source_unresolvable" // manifest line pattern not recognized
	SkipSourceMismatch     SkipCategory = "source_mismatch"     // current value not found where expected
	SkipNoChange           SkipCategory = "no_change"           // replacement produced no diff
	SkipNoGoSource         SkipCategory = "no_go_source"        // content/tooling module, no Go source
	SkipReplaceDirective   SkipCategory = "replace_directive"   // go.mod replace present
	SkipNoFixedVersion     SkipCategory = "no_fixed_version"    // advisory with no known fixed version
	SkipConflict           SkipCategory = "conflict"            // conflicting desired versions within a module
	SkipWildcardManaged    SkipCategory = "wildcard_managed"    // wildcard constraint auto-resolves; nothing to rewrite

	// SkipOther is the fallback for a decision not yet given its own category.
	SkipOther SkipCategory = "other"
)
