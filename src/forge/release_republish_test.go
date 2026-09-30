package forge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A publishing re-publish toggles draft:true then applies the content (draft:false).
func TestRepublishWithDraftToggle_TogglesThenApplies(t *testing.T) {
	var bodies []map[string]interface{}
	patch := func(ctx context.Context, body map[string]interface{}, out interface{}) error {
		bodies = append(bodies, body)
		return nil
	}
	content := map[string]interface{}{"body": "notes", "draft": false}
	if err := republishWithDraftToggle(context.Background(), "v1.2.3", true, content, nil, patch); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d patches, want 2 (draft:true then content)", len(bodies))
	}
	if bodies[0]["draft"] != true {
		t.Errorf("first patch draft = %v, want true (re-draft to refresh publish time)", bodies[0]["draft"])
	}
	if bodies[1]["draft"] != false || bodies[1]["body"] != "notes" {
		t.Errorf("second patch = %v, want the content with draft:false", bodies[1])
	}
}

// A draft target has no publish time to refresh, so no toggle — content applied directly.
func TestRepublishWithDraftToggle_NonPublishingSkipsToggle(t *testing.T) {
	var bodies []map[string]interface{}
	patch := func(ctx context.Context, body map[string]interface{}, out interface{}) error {
		bodies = append(bodies, body)
		return nil
	}
	content := map[string]interface{}{"body": "notes", "draft": true}
	if err := republishWithDraftToggle(context.Background(), "v1.2.3", false, content, nil, patch); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d patches, want 1 (no draft-toggle for a draft target)", len(bodies))
	}
}

// If the content PATCH fails after the release was drafted, the helper must best-effort
// re-publish so the release is not left hidden — and report the CONTENT error, not the
// drafted-state error (the restore succeeded).
func TestRepublishWithDraftToggle_RestoresVisibilityOnContentFailure(t *testing.T) {
	var bodies []map[string]interface{}
	contentErr := errors.New("boom")
	patch := func(ctx context.Context, body map[string]interface{}, out interface{}) error {
		bodies = append(bodies, body)
		if body["body"] == "notes" { // the content PATCH
			return contentErr
		}
		return nil // draft toggles succeed
	}
	err := republishWithDraftToggle(context.Background(), "v1.2.3", true,
		map[string]interface{}{"body": "notes", "draft": false}, nil, patch)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "left as a DRAFT") {
		t.Errorf("must NOT report drafted state when the restore succeeded: %v", err)
	}
	if !errors.Is(err, contentErr) {
		t.Errorf("error should wrap the content failure, got: %v", err)
	}
	if len(bodies) != 3 { // draft:true, content(fail), restore draft:false
		t.Fatalf("got %d patches, want 3 (draft:true, content, restore draft:false): %v", len(bodies), bodies)
	}
	if bodies[2]["draft"] != false {
		t.Errorf("restore patch draft = %v, want false", bodies[2]["draft"])
	}
}

// If BOTH the content PATCH and the restore fail, the release is genuinely left drafted
// (invisible) — the error MUST say so explicitly so it isn't silent damage.
func TestRepublishWithDraftToggle_NamesDraftStateWhenRestoreFails(t *testing.T) {
	patch := func(ctx context.Context, body map[string]interface{}, out interface{}) error {
		if d, _ := body["draft"].(bool); d {
			return nil // draft:true succeeds
		}
		return errors.New("network down") // content (draft:false) AND restore (draft:false) fail
	}
	err := republishWithDraftToggle(context.Background(), "v1.2.3", true,
		map[string]interface{}{"body": "notes", "draft": false}, nil, patch)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "left as a DRAFT") || !strings.Contains(err.Error(), "not publicly visible") {
		t.Errorf("error must name the drafted state explicitly, got: %v", err)
	}
}

// republishUpdate drops the immutable identity fields and detects draft state.
func TestRepublishUpdate_DropsIdentityAndDetectsDraft(t *testing.T) {
	update, publishing := republishUpdate(map[string]interface{}{
		"tag_name": "v1.2.3", "target_commitish": "abc", "name": "v1.2.3", "body": "n", "draft": false,
	})
	if !publishing {
		t.Error("draft:false must be treated as publishing")
	}
	if _, ok := update["tag_name"]; ok {
		t.Error("tag_name must be dropped")
	}
	if _, ok := update["target_commitish"]; ok {
		t.Error("target_commitish must be dropped")
	}
	if update["body"] != "n" {
		t.Error("content fields must survive")
	}

	_, publishing = republishUpdate(map[string]interface{}{"draft": true})
	if publishing {
		t.Error("draft:true must NOT be treated as publishing")
	}
}
