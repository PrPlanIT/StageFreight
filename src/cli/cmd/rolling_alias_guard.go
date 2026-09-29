package cmd

import (
	"context"
	"fmt"
	"strings"

	masterminds "github.com/Masterminds/semver/v3"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// ── rolling alias monotonicity ────────────────────────────────────────────
//
// A rolling alias (latest-dev and its kin) is a promise: it names the NEWEST build
// on its channel. refreshRollingRelease is a delete-then-create, so re-publishing an
// older commit's build used to drag the alias backwards — a silent downgrade for
// anyone pulling it.
//
// The guard applies ONLY where an alias release is refreshed, which is the channel
// path (a target declaring `tag:`). A stable tag push never reaches it, so a
// deliberate `sf tag` can never be blocked by this. Ties are allowed: only a
// STRICTLY newer incumbent holds, so re-cutting the same version always proceeds.

const (
	aliasMarkerPrefix = "<!-- sf:alias v="
	aliasMarkerSuffix = " -->"
)

// aliasMarker records the build an alias release points at, so the next refresh can
// tell whether it would move the alias backwards.
func aliasMarker(version string) string {
	return aliasMarkerPrefix + version + aliasMarkerSuffix
}

// readAliasVersion extracts the recorded version from an alias release body.
func readAliasVersion(body string) (string, bool) {
	i := strings.Index(body, aliasMarkerPrefix)
	if i < 0 {
		return "", false
	}
	rest := body[i+len(aliasMarkerPrefix):]
	j := strings.Index(rest, aliasMarkerSuffix)
	if j < 0 {
		return "", false
	}
	v := strings.TrimSpace(rest[:j])
	return v, v != ""
}

// aliasHolds reports whether refreshing `alias` would move it BACKWARDS, and to what.
//
// Fail-open by design: an alias with no marker, an unparseable version on either side,
// or an unreadable forge yields no hold. A guard that cannot prove a regression must
// not block a publish — the cost of a false block is a stuck channel, which is worse
// than the downgrade it is trying to prevent.
func aliasHolds(ctx context.Context, fc forge.Forge, alias, newVersion string) (incumbent string, hold bool) {
	rels, err := fc.ListReleases(ctx)
	if err != nil {
		return "", false
	}
	var body string
	for _, r := range rels {
		if r.TagName == alias {
			body = r.Description
			break
		}
	}
	if body == "" {
		return "", false
	}
	recorded, ok := readAliasVersion(body)
	if !ok {
		return "", false // unmanaged alias — adopt it on this refresh
	}
	if holdsFor(recorded, newVersion) {
		return recorded, true
	}
	return "", false
}

// holdsFor reports whether publishing newVersion would move an alias currently at
// recorded BACKWARDS. Strictly-newer only, so an identical version — a deliberate
// re-cut of the same tag — always proceeds. Unparseable on either side fails open.
func holdsFor(recorded, newVersion string) bool {
	cur, curErr := masterminds.NewVersion(recorded)
	next, nextErr := masterminds.NewVersion(newVersion)
	if curErr != nil || nextErr != nil {
		return false
	}
	return cur.GreaterThan(next)
}

// heldAliasReason is the message shown when an alias is withheld. Fixed shape: two
// version strings, so it cannot grow with the repository.
func heldAliasReason(incumbent string) error {
	return fmt.Errorf("held — already points at %s, newer than this build; --force-alias overrides", incumbent)
}

// releaseForceAlias opts into moving a rolling alias backwards — a deliberate
// rollback. Off by default: the alias only advances.
var releaseForceAlias bool
