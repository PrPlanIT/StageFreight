package forge

import (
	"context"
	"fmt"
)

// releasePatchFunc applies a JSON body to an already-resolved release, decoding the
// response into out (which may be nil). Each forge supplies one bound to its own
// doJSON/apiURL and the resolved release id.
type releasePatchFunc func(ctx context.Context, body map[string]interface{}, out interface{}) error

// republishWithDraftToggle updates an existing release in place AND re-stamps its publish
// timestamp. Both GitHub and Gitea keep a release's ORIGINAL publish date across a plain
// content PATCH, so re-cutting a release would keep showing its first publish date rather
// than this one. Toggling the release to draft and back re-stamps it to now (verified
// against both live APIs) while preserving the release id, assets, and URL — the reason this
// path updates in place instead of delete-and-recreating.
//
// FAIL-SAFE: a drafted release is invisible to anonymous users and its assets stop
// resolving, so if the content PATCH fails AFTER the release was drafted, this best-effort
// re-publishes it (draft:false) so it is at least visible again; if that restore also fails,
// the returned error names the drafted state explicitly, because otherwise a transient error
// would silently leave a published release hidden with no hint in the log.
//
// content MUST carry draft:false. publishing is true only when the target is a published
// (non-draft) release — the sole case a draft-toggle applies; a target that stays a draft
// has no publish time to refresh, so content is applied directly. ref names the release
// (its tag) for error messages.
func republishWithDraftToggle(
	ctx context.Context,
	ref string,
	publishing bool,
	content map[string]interface{},
	out interface{},
	patch releasePatchFunc,
) error {
	if publishing {
		if err := patch(ctx, map[string]interface{}{"draft": true}, nil); err != nil {
			return fmt.Errorf("re-drafting release %s to refresh publish time: %w", ref, err)
		}
	}

	// The content PATCH carries draft:false, so — following the draft:true above — it both
	// updates the release and re-publishes it; that draft->published transition is what
	// re-stamps the publish timestamp.
	if err := patch(ctx, content, out); err != nil {
		if publishing {
			if rErr := patch(ctx, map[string]interface{}{"draft": false}, nil); rErr != nil {
				return fmt.Errorf("updating existing release %s: %w — AND it is left as a DRAFT "+
					"(re-publish failed: %v); it is not publicly visible until re-published", ref, err, rErr)
			}
		}
		return fmt.Errorf("updating existing release %s: %w", ref, err)
	}
	return nil
}

// republishUpdate copies a create payload into an in-place update payload (dropping the
// immutable identity fields) and reports whether the target is a published release. Shared
// by the GitHub and Gitea re-publish paths so both drop the same fields and detect draft
// state identically.
func republishUpdate(payload map[string]interface{}) (update map[string]interface{}, publishing bool) {
	update = make(map[string]interface{}, len(payload))
	for k, v := range payload {
		// tag_name and target_commitish are fixed once a release exists; re-sending them is
		// at best ignored and at worst rejected.
		if k == "tag_name" || k == "target_commitish" {
			continue
		}
		update[k] = v
	}
	publishing = true
	if d, ok := update["draft"].(bool); ok && d {
		publishing = false
	}
	return update, publishing
}
