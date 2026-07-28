package mirror

import "context"

// UpsertReleases authors releases onto dst — create-or-update, content-aware,
// from local build files (DesiredAsset.Local). A release that already exists is
// converged (notes drift + changed assets replaced), never duplicated; an
// unchanged one is a fingerprint no-op. There is no prune (authoring adds; it
// does not manage a set), and a foreign release with the same tag is left
// untouched (never clobbered) — exactly like the mirror reconciler.
//
// This is the `sf release create` engine: idempotent by construction, so a
// re-run on the same commit changes nothing, and fixing a bad binary is just a
// re-run with the new file.
func UpsertReleases(ctx context.Context, dst releaseForge, desired []DesiredRelease) (*Result, error) {
	return ReconcileReleases(ctx, nil, dst, desired, Options{})
}

// destroyer is the delete side of a forge.
type destroyer interface {
	DeleteRelease(ctx context.Context, tag string) error
}

// DestroyRelease removes a release by tag across the given forges (primary +
// mirrors). Deliberate and explicit — the `sf release destroy` engine. No
// tombstone: once it's gone from the source, a later mirror reconcile has
// nothing to resurrect (the desired set no longer contains it). Errors are
// collected per forge, not fatal, so one unreachable mirror doesn't block the rest.
func DestroyRelease(ctx context.Context, tag string, forges ...destroyer) []error {
	var errs []error
	for _, f := range forges {
		if err := f.DeleteRelease(ctx, tag); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
