package dependency

import (
	"strings"
	"time"

	"github.com/PrPlanIT/StageFreight/src/supplychain"
)

// SkippedDep records a dependency that was not updated, with a reason.
type SkippedDep struct {
	Dep    supplychain.Dependency
	Reason string
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
	supplychain.EcosystemPip:           false,
}

// FilterUpdateCandidates separates deps into actionable candidates and skipped.
// Each skipped dep gets an explicit reason string.
func FilterUpdateCandidates(deps []supplychain.Dependency, cfg UpdateConfig, trackedFiles map[string]bool) (candidates []supplychain.Dependency, skipped []SkippedDep) {
	ecosystemFilter := make(map[string]bool, len(cfg.Ecosystems))
	for _, e := range cfg.Ecosystems {
		ecosystemFilter[e] = true
	}

	for _, dep := range deps {
		if reason := skipReason(dep, cfg, ecosystemFilter, trackedFiles); reason != "" {
			skipped = append(skipped, SkippedDep{Dep: dep, Reason: reason})
			continue
		}
		candidates = append(candidates, dep)
	}
	return
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

func skipReason(dep supplychain.Dependency, cfg UpdateConfig, ecosystemFilter map[string]bool, trackedFiles map[string]bool) string {
	// Vulnerability remediation is a FLOOR, not a policy preference — a vulnerable indirect is
	// remediated under EVERY policy. The transitive-management assumption (bump a direct
	// parent → `go mod tidy` pulls the fix) has demonstrably FAILED for it: nothing on the
	// direct graph requires a fixed version. So it must be pinned / parent-bumped by the Go
	// vuln remediator — unlike an ordinary indirect it is NOT skipped, and it is exempt from
	// the "unresolved" and "up to date" checks below (its Latest is empty by design; the
	// target is the advisory's FixedIn). It still honors the ecosystem / auto-updatable /
	// tracked-file gates. The policy string governs only FRESHNESS of non-vulnerable deps
	// (see the security-only gate below) — never whether a known vulnerability gets fixed.
	vulnIndirect := dep.Indirect && len(dep.Vulnerabilities) > 0

	// Non-vulnerable indirect deps are managed transitively (go mod tidy after direct
	// updates), never updated directly, so resolution deliberately skips them — leaving
	// Latest empty. Classify BEFORE the unresolved check, or they masquerade as "could
	// not verify" when nothing was ever attempted.
	if dep.Indirect && !vulnIndirect {
		return "indirect dependency"
	}

	// Unresolved: the latest version could NOT be verified (registry failure, empty
	// response). Inability to determine state must never collapse into verified
	// healthy — "couldn't check" is a different operational condition than "current".
	// (A vuln-indirect legitimately has an empty Latest — remediation targets the
	// advisory FixedIn, not Latest — so it is exempt.)
	if !vulnIndirect && (dep.ResolutionError != "" || dep.Latest == "") {
		return "unresolved (could not verify latest version)"
	}

	// Autonomous remediation advances to the COMPATIBLE target (UpdateTarget), not the
	// raw registry maximum. When the compatible target equals Current there is nothing
	// safe to apply: it's up to date — unless a higher version exists OUT of the
	// constraint range, which is a constraint-expanding major upgrade held for review
	// (review-domain, may need feature renames / API migration), never auto-applied.
	// (A vuln-indirect is exempt: UpdateTarget derives from an empty Latest.)
	if !vulnIndirect && dep.UpdateTarget() == dep.Current {
		if dep.MajorAvailable() {
			return "major upgrade held — review (" + dep.Latest + " out of range)"
		}
		return "up to date"
	}

	// Ecosystem filter
	if len(ecosystemFilter) > 0 && !ecosystemFilter[dep.Ecosystem] {
		return "ecosystem filtered out"
	}

	// Ecosystem not auto-updatable
	if updatable, known := autoUpdatableEcosystems[dep.Ecosystem]; known && !updatable {
		return "ecosystem not auto-updatable"
	}

	// FRESHNESS AXIS — the only thing the policy string decides: `all` pursues freshness on
	// non-vulnerable deps; `security` does not (it has already remediated every vulnerability
	// above, and a non-vuln dep has nothing to fix). Vulnerable deps never reach here as a
	// skip — they were kept as candidates by the floor above.
	if cfg.Policy == "security" && len(dep.Vulnerabilities) == 0 {
		return "no CVE (security-only policy)"
	}

	// File not tracked by git
	if trackedFiles != nil && !trackedFiles[dep.File] {
		return "file not tracked by git"
	}

	// Docker-image specific skips
	if dep.Ecosystem == supplychain.EcosystemDockerImage {
		if reason := dockerImageSkipReason(dep); reason != "" {
			return reason
		}
	}

	return ""
}

func dockerImageSkipReason(dep supplychain.Dependency) string {
	name := dep.Name

	// Digest-pinned images
	if strings.Contains(name, "@sha256:") {
		return "digest-pinned image"
	}

	// ARG-based dynamic base images
	if strings.ContainsAny(name, "$") {
		return "ARG-based dynamic base image"
	}

	// Determine tag: split on last : after the last /
	tag := extractTag(name)
	if tag == "" {
		return "untagged image"
	}
	if tag == "latest" {
		return "latest tag"
	}

	return ""
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
