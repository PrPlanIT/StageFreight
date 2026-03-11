package registry

import (
	"context"
	"testing"
	"time"

	"github.com/sofmeright/stagefreight/src/config"
)

// fakeRegistry implements Registry for testing.
type fakeRegistry struct {
	tags    []TagInfo
	deleted []string
}

func (f *fakeRegistry) Provider() string { return "fake" }

func (f *fakeRegistry) ListTags(_ context.Context, _ string) ([]TagInfo, error) {
	return f.tags, nil
}

func (f *fakeRegistry) DeleteTag(_ context.Context, _ string, tag string) error {
	f.deleted = append(f.deleted, tag)
	return nil
}

func (f *fakeRegistry) UpdateDescription(_ context.Context, _, _, _ string) error {
	return nil
}

func TestApplyRetention_DigestProtection(t *testing.T) {
	now := time.Now()

	// Scenario: dev-a through dev-e (oldest to newest) + latest-dev.
	// latest-dev shares digest with dev-e (the newest dev tag).
	// keep_last=2 means only dev-d and dev-e survive by policy.
	// dev-a, dev-b, dev-c are candidates for deletion.
	// But we also protect latest-dev, so its shared digest protects dev-e
	// (already kept by keep_last) and would protect any tag sharing that digest.
	//
	// Additionally: dev-c shares digest with latest-dev to test skip behavior.
	reg := &fakeRegistry{
		tags: []TagInfo{
			{Name: "dev-e", Digest: "sha256:eeee", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "latest-dev", Digest: "sha256:eeee", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "dev-d", Digest: "sha256:dddd", CreatedAt: now.Add(-2 * time.Hour)},
			{Name: "dev-c", Digest: "sha256:eeee", CreatedAt: now.Add(-3 * time.Hour)}, // shares digest with latest-dev
			{Name: "dev-b", Digest: "sha256:bbbb", CreatedAt: now.Add(-4 * time.Hour)},
			{Name: "dev-a", Digest: "sha256:aaaa", CreatedAt: now.Add(-5 * time.Hour)},
		},
	}

	policy := config.RetentionPolicy{
		KeepLast: 2,
		Protect:  []string{"latest-dev"},
	}

	// Tag patterns: only dev-* tags are retention candidates.
	tagPatterns := []string{"^dev-"}

	result, err := ApplyRetention(context.Background(), reg, "prplanit/test", tagPatterns, policy)
	if err != nil {
		t.Fatalf("ApplyRetention() error: %v", err)
	}

	// Matched: dev-a, dev-b, dev-c, dev-d, dev-e (5 dev tags, latest-dev excluded by pattern)
	if result.Matched != 5 {
		t.Errorf("Matched = %d; want 5", result.Matched)
	}

	// Kept: dev-e (keep_last #1), dev-d (keep_last #2) = 2
	if result.Kept != 2 {
		t.Errorf("Kept = %d; want 2", result.Kept)
	}

	// dev-c should be SKIPPED (digest shared with protected latest-dev)
	if len(result.Skipped) != 1 || result.Skipped[0] != "dev-c" {
		t.Errorf("Skipped = %v; want [dev-c]", result.Skipped)
	}

	// dev-a and dev-b should be deleted (different digests, beyond keep_last)
	wantDeleted := map[string]bool{"dev-a": true, "dev-b": true}
	if len(result.Deleted) != 2 {
		t.Errorf("Deleted count = %d; want 2, got %v", len(result.Deleted), result.Deleted)
	}
	for _, d := range result.Deleted {
		if !wantDeleted[d] {
			t.Errorf("unexpected deletion: %s", d)
		}
	}

	// Verify actual registry calls
	regDeleted := map[string]bool{}
	for _, d := range reg.deleted {
		regDeleted[d] = true
	}
	if regDeleted["dev-c"] {
		t.Error("dev-c was deleted from registry despite sharing digest with protected tag")
	}
	if !regDeleted["dev-a"] || !regDeleted["dev-b"] {
		t.Errorf("expected dev-a and dev-b deleted from registry, got %v", reg.deleted)
	}

	if len(result.Errors) != 0 {
		t.Errorf("unexpected errors: %v", result.Errors)
	}
}

func TestApplyRetention_ProtectedTagKept(t *testing.T) {
	now := time.Now()

	// Minimal test: latest-dev is a candidate tag, policy protects it.
	reg := &fakeRegistry{
		tags: []TagInfo{
			{Name: "dev-b", Digest: "sha256:bbbb", CreatedAt: now.Add(-1 * time.Hour)},
			{Name: "dev-a", Digest: "sha256:aaaa", CreatedAt: now.Add(-2 * time.Hour)},
			{Name: "latest-dev", Digest: "sha256:bbbb", CreatedAt: now.Add(-1 * time.Hour)},
		},
	}

	policy := config.RetentionPolicy{
		KeepLast: 1,
		Protect:  []string{"latest-dev"},
	}

	result, err := ApplyRetention(context.Background(), reg, "prplanit/test", nil, policy)
	if err != nil {
		t.Fatalf("ApplyRetention() error: %v", err)
	}

	// dev-b kept (keep_last=1), latest-dev kept (protected).
	// dev-a would be deleted but shares no digest protection → deleted.
	if result.Kept != 2 {
		t.Errorf("Kept = %d; want 2", result.Kept)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != "dev-a" {
		t.Errorf("Deleted = %v; want [dev-a]", result.Deleted)
	}
}
