package dependency

import (
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
	depversion "github.com/PrPlanIT/StageFreight/src/supplychain/version"
)

// SkippedDep records a dependency that was not updated. Category is the typed
// classification of the decision (the source of truth); Reason is its human-readable
// presentation, kept verbatim so existing output is unchanged.
type SkippedDep struct {
	Dep      supplychain.Dependency
	Category SkipCategory
	Reason   string
}

// autoUpdatableEcosystems defines which ecosystems can be automatically updated.
var autoUpdatableEcosystems = map[string]bool{
	supplychain.EcosystemDockerImage:   true,
	supplychain.EcosystemGitHubRelease: true,
	supplychain.EcosystemGoMod:         true,
	supplychain.EcosystemToolchain:     true,
	supplychain.EcosystemCargo:         true,
	supplychain.EcosystemNpm:           false,
	supplychain.EcosystemAlpineAPK:     false,
	supplychain.EcosystemDebianAPT:     false,
	supplychain.EcosystemPip:           true, // requirements.txt exact pins (apply_pip.go); Pipfile/poetry skipped there
}

// FilterUpdateCandidates separates deps into actionable candidates and skipped.
// Each skipped dep gets an explicit reason string.
func FilterUpdateCandidates(deps []supplychain.Dependency, cfg UpdateConfig, trackedFiles map[string]bool) (candidates []supplychain.Dependency, skipped []SkippedDep) {
	ecosystemFilter := make(map[string]bool, len(cfg.Ecosystems))
	for _, e := range cfg.Ecosystems {
		ecosystemFilter[e] = true
	}

	for _, dep := range deps {
		// One policy evaluation per dep — the updater, remediation, and freshness all
		// read this same CandidateSet (see candidate.go). Here the updater consumes it.
		cs := Construct(dep, cfg, ecosystemFilter, trackedFiles)
		if !cs.Eligible {
			skipped = append(skipped, SkippedDep{Dep: dep, Category: cs.Category, Reason: cs.Reason})
			continue
		}
		if cs.ResolvedTarget != "" {
			dep.ResolvedTarget = cs.ResolvedTarget // ceiling re-target → apply advances here
		}
		candidates = append(candidates, dep)
	}
	return
}

// ceilingRetarget returns a lower in-ceiling update target when a dependency's
// natural target would be HELD by the max_update ceiling but the registry lists a
// smaller in-ceiling upgrade — e.g. patch-lock selecting the newest patch of the
// current minor rather than holding on a minor bump. Returns "" when the natural
// target is already within the ceiling (nothing to re-target) or no in-ceiling
// upgrade is available (the dep is genuinely held). Requires AvailableVersions,
// which discovery populates only under patch-lock (and free for cargo).
func ceilingRetarget(dep supplychain.Dependency, maxUpdate string) string {
	if !updateTypeExceedsCeiling(updateType(dep.Current, dep.UpdateTarget()), maxUpdate) {
		return "" // natural target already fits — it IS the best in-ceiling choice
	}
	t := depversion.CeilingTarget(dep.Current, dep.AvailableVersions, maxUpdate, dep.Ecosystem)
	if t == dep.Current {
		return ""
	}
	return t
}

// ApplyIgnores strips advisories the operator has explicitly accepted (dependency.ignore)
// from each dependency's Vulnerabilities — UNLESS the ignore has lapsed past its `until`
// date, in which case the advisory re-surfaces (accepted risk is never permanent). IDs
// match case-insensitively. A malformed or expired `until` is treated as expired: it must
// never silently drop a real finding. A dependency whose every advisory is ignored is
// left with zero vulnerabilities and thus falls out of security-policy scope.
func ApplyIgnores(deps []supplychain.Dependency, ignores []VulnIgnore, now time.Time) []supplychain.Dependency {
	active := make(map[string]bool, len(ignores))
	for _, ig := range ignores {
		id := strings.ToUpper(strings.TrimSpace(ig.ID))
		if id == "" {
			continue
		}
		if u := strings.TrimSpace(ig.Until); u != "" {
			t, err := time.Parse("2006-01-02", u)
			if err != nil || !now.Before(t) {
				continue // malformed or lapsed → does not suppress
			}
		}
		active[id] = true
	}
	if len(active) == 0 {
		return deps
	}
	out := make([]supplychain.Dependency, len(deps))
	copy(out, deps)
	for i := range out {
		if len(out[i].Vulnerabilities) == 0 {
			continue
		}
		kept := out[i].Vulnerabilities[:0:0] // fresh backing array — never mutate the input's
		for _, v := range out[i].Vulnerabilities {
			if active[strings.ToUpper(strings.TrimSpace(v.ID))] {
				continue
			}
			kept = append(kept, v)
		}
		out[i].Vulnerabilities = kept
	}
	return out
}

// skipReason evaluates a dependency and returns the typed decision plus its
// human-readable reason. SkipNone (empty category) means "not skipped — a candidate".
func skipReason(dep supplychain.Dependency, cfg UpdateConfig, ecosystemFilter map[string]bool, trackedFiles map[string]bool) (SkipCategory, string) {
	// Vulnerability remediation is a FLOOR, not a policy preference — a vulnerable indirect is
	// remediated under EVERY policy. The transitive-management assumption (bump a direct
	// parent → `go mod tidy` pulls the fix) has demonstrably FAILED for it: nothing on the
	// direct graph requires a fixed version. So it must be pinned / parent-bumped by the Go
	// vuln remediator — unlike an ordinary indirect it is NOT skipped, and it is exempt from
	// the "unresolved" and "up to date" checks below (its Latest is empty by design; the
	// target is the advisory's FixedIn). It still honors the ecosystem / auto-updatable /
	// tracked-file gates. The policy string governs only FRESHNESS of non-vulnerable deps
	// (see the security-only gate below) — never whether a known vulnerability gets fixed.
	// A native selection directive (e.g. go.mod replace) already governs this dep:
	// the toolchain has chosen its version, so StageFreight proposes no update. Checked
	// FIRST so a pinned dep with no resolved Latest is not mislabeled "unresolved".
	if dep.Pinned != "" {
		return SkipReplaceDirective, "replace directive present"
	}

	vulnIndirect := dep.Indirect && len(dep.Vulnerabilities) > 0

	// Non-vulnerable indirect deps are managed transitively (go mod tidy after direct
	// updates), never updated directly, so resolution deliberately skips them — leaving
	// Latest empty. Classify BEFORE the unresolved check, or they masquerade as "could
	// not verify" when nothing was ever attempted.
	if dep.Indirect && !vulnIndirect {
		return SkipIndirect, "indirect dependency"
	}

	// Unresolved: the latest version could NOT be verified (registry failure, empty
	// response). Inability to determine state must never collapse into verified
	// healthy — "couldn't check" is a different operational condition than "current".
	// (A vuln-indirect legitimately has an empty Latest — remediation targets the
	// advisory FixedIn, not Latest — so it is exempt.)
	if !vulnIndirect && (dep.ResolutionError != "" || dep.Latest == "") {
		return SkipUnresolved, "unresolved (could not verify latest version)"
	}

	// Autonomous remediation advances to the COMPATIBLE target (UpdateTarget), not the
	// raw registry maximum. When the compatible target equals Current there is nothing
	// safe to apply: it's up to date — unless a higher version exists OUT of the
	// constraint range, which is a constraint-expanding major upgrade held for review
	// (review-domain, may need feature renames / API migration), never auto-applied.
	// (A vuln-indirect is exempt: UpdateTarget derives from an empty Latest.)
	if !vulnIndirect && dep.UpdateTarget() == dep.Current {
		if dep.MajorAvailable() {
			return SkipMajorHeld, "major upgrade held — review (" + dep.Latest + " out of range)"
		}
		return SkipUpToDate, "up to date"
	}

	// Ecosystem filter
	if len(ecosystemFilter) > 0 && !ecosystemFilter[dep.Ecosystem] {
		return SkipEcosystemFiltered, "ecosystem filtered out"
	}

	// Ecosystem not auto-updatable
	if updatable, known := autoUpdatableEcosystems[dep.Ecosystem]; known && !updatable {
		return SkipNotAutoUpdatable, "ecosystem not auto-updatable"
	}

	// FRESHNESS AXIS — the only thing the policy string decides: `all` pursues freshness on
	// non-vulnerable deps; `security` does not (it has already remediated every vulnerability
	// above, and a non-vuln dep has nothing to fix). Vulnerable deps never reach here as a
	// skip — they were kept as candidates by the floor above.
	if cfg.Policy == "security" && len(dep.Vulnerabilities) == 0 {
		return SkipSecurityOnly, "no CVE (security-only policy)"
	}

	// UPDATE-TYPE CEILING (max_update) — caps how far a NON-vulnerable dep may
	// move within its constraint range. A vulnerable dep is exempt: the
	// remediation floor overrides the ceiling. Out-of-range majors are already
	// held for review above regardless of ceiling (auto-applying a major is unsafe
	// — e.g. a Go v2+ module-path change); this gate adds the tighter in-range
	// caps, e.g. "patch" holds an in-range minor so only patches land.
	if len(dep.Vulnerabilities) == 0 && updateTypeExceedsCeiling(updateType(dep.Current, dep.UpdateTarget()), cfg.MaxUpdate) {
		// The natural target exceeds the ceiling. Re-target to the highest in-ceiling
		// version if the registry lists one (e.g. patch-lock grabbing the newest patch
		// of the current minor); only HOLD when no in-ceiling upgrade exists. The
		// re-target itself is recorded on the candidate in FilterUpdateCandidates.
		if ceilingRetarget(dep, cfg.MaxUpdate) == "" {
			label := cfg.MaxUpdate
			if label == "" {
				label = "major"
			}
			return SkipCeilingExceeded, "exceeds max_update ceiling (" + label + ")"
		}
	}

	// File not tracked by git
	if trackedFiles != nil && !trackedFiles[dep.File] {
		return SkipFileUntracked, "file not tracked by git"
	}

	// Docker-image specific skips
	if dep.Ecosystem == supplychain.EcosystemDockerImage {
		if cat, reason := dockerImageSkipReason(dep); cat != SkipNone {
			return cat, reason
		}
	}

	return SkipNone, ""
}

func dockerImageSkipReason(dep supplychain.Dependency) (SkipCategory, string) {
	name := dep.Name

	// Digest-pinned images
	if strings.Contains(name, "@sha256:") {
		return SkipDockerConstraint, "digest-pinned image"
	}

	// ARG-based dynamic base images
	if strings.ContainsAny(name, "$") {
		return SkipDockerConstraint, "ARG-based dynamic base image"
	}

	// Determine tag: split on last : after the last /
	tag := extractTag(name)
	if tag == "" {
		return SkipDockerConstraint, "untagged image"
	}
	if tag == "latest" {
		return SkipDockerConstraint, "latest tag"
	}

	return SkipNone, ""
}

// extractTag extracts the tag portion from a Docker image reference.
// It splits on the last : after the last / to avoid host:port confusion.
func extractTag(image string) string {
	// Find the last /
	lastSlash := strings.LastIndex(image, "/")
	nameAndTag := image
	if lastSlash >= 0 {
		nameAndTag = image[lastSlash+1:]
	}

	// Find : in the portion after the last /
	colonIdx := strings.LastIndex(nameAndTag, ":")
	if colonIdx < 0 {
		return ""
	}
	return nameAndTag[colonIdx+1:]
}

// updateTypeExceedsCeiling reports whether an update of type ut (from
// updateType(): major/minor/patch/tag/security) is beyond the max_update
// ceiling. Empty ceiling defaults to "major" — a no-op (nothing exceeds it), so an
// unset ceiling never holds anything the ecosystem's own model would allow. patch <
// minor < major; tag and security are treated as patch-level (revision / floor bumps).
func updateTypeExceedsCeiling(ut, maxUpdate string) bool {
	rank := func(t string) int {
		switch t {
		case "major":
			return 3
		case "minor":
			return 2
		default: // patch, tag, security, unknown
			return 1
		}
	}
	ceiling := 3 // default (empty): major — no ceiling beyond the ecosystem compat model
	switch maxUpdate {
	case "patch":
		ceiling = 1
	case "minor":
		ceiling = 2
	}
	return rank(ut) > ceiling
}
