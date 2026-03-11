package registry

import (
	"context"
	"fmt"

	"github.com/sofmeright/stagefreight/src/config"
	"github.com/sofmeright/stagefreight/src/retention"
)

// RetentionResult captures what the retention engine did.
type RetentionResult struct {
	Provider string
	Repo     string
	Matched  int      // tags matching the pattern set
	Kept     int      // tags kept by policy
	Deleted  []string // tags successfully deleted
	Skipped  []string // tags skipped (digest shared with protected tag)
	Errors   []error  // errors from individual deletes
}

// ApplyRetention lists all tags on the registry, filters them by the given
// tag patterns (using config.MatchPatterns with full !/OR/AND semantics),
// sorts by creation time descending, and applies restic-style retention
// policies to decide which tags to keep.
//
// Tags whose digest is shared with a protected tag are skipped during
// deletion to prevent breaking rolling tags like latest-dev.
//
// Policies are additive — a tag survives if ANY policy wants to keep it:
//   - keep_last N:    keep the N most recent
//   - keep_daily N:   keep one per day for the last N days
//   - keep_weekly N:  keep one per week for the last N weeks
//   - keep_monthly N: keep one per month for the last N months
//   - keep_yearly N:  keep one per year for the last N years
//
// tagPatterns uses the same syntax as branches/git_tags in the config:
//
//	["^dev-"]              → only tags starting with "dev-"
//	["^dev-", "!^dev-keep"]→ dev- tags, excluding dev-keep*
//	[]                     → ALL tags are candidates (dangerous, use with care)
func ApplyRetention(ctx context.Context, reg Registry, repo string, tagPatterns []string, policy config.RetentionPolicy) (*RetentionResult, error) {
	// List tags once and build digest maps for shared-digest protection.
	tags, err := reg.ListTags(ctx, repo)
	if err != nil {
		return &RetentionResult{
			Provider: reg.Provider(),
			Repo:     repo,
		}, fmt.Errorf("listing tags: %w", err)
	}

	// Build tag→digest map and items for the retention engine.
	tagDigests := make(map[string]string, len(tags))
	items := make([]retention.Item, len(tags))
	for i, t := range tags {
		tagDigests[t.Name] = t.Digest
		items[i] = retention.Item{
			Name:      t.Name,
			CreatedAt: t.CreatedAt,
		}
	}

	// Identify digests belonging to protected tags.
	protectPatterns := retention.TemplatesToPatterns(policy.Protect)
	protectedDigests := make(map[string]bool)
	for _, t := range tags {
		if t.Digest != "" && len(protectPatterns) > 0 && config.MatchPatterns(protectPatterns, t.Name) {
			protectedDigests[t.Digest] = true
		}
	}

	store := &digestAwareStore{
		items:            items,
		reg:              reg,
		repo:             repo,
		tagDigests:       tagDigests,
		protectedDigests: protectedDigests,
	}

	patterns := retention.TemplatesToPatterns(tagPatterns)
	result, err := retention.Apply(ctx, store, patterns, policy)
	if err != nil {
		return &RetentionResult{
			Provider: reg.Provider(),
			Repo:     repo,
		}, err
	}

	return &RetentionResult{
		Provider: reg.Provider(),
		Repo:     repo,
		Matched:  result.Matched,
		Kept:     result.Kept,
		Deleted:  result.Deleted,
		Skipped:  result.Skipped,
		Errors:   result.Errors,
	}, nil
}

// digestAwareStore adapts the Registry interface to the retention.Store
// interface with digest-based protection: if a candidate tag shares a
// digest with any protected tag, deletion is skipped (reported, not errored).
type digestAwareStore struct {
	items            []retention.Item
	reg              Registry
	repo             string
	tagDigests       map[string]string // tag name → digest
	protectedDigests map[string]bool   // digests that must not be removed
}

func (s *digestAwareStore) List(_ context.Context) ([]retention.Item, error) {
	return s.items, nil
}

func (s *digestAwareStore) Delete(ctx context.Context, name string) error {
	digest := s.tagDigests[name]
	if digest != "" && s.protectedDigests[digest] {
		return &SkippedError{Tag: name}
	}
	return s.reg.DeleteTag(ctx, s.repo, name)
}

// SkippedError is a sentinel indicating a tag was skipped because its
// digest is shared with a protected tag.
type SkippedError struct {
	Tag string
}

func (e *SkippedError) Error() string {
	return fmt.Sprintf("skipped %s: digest shared with protected tag", e.Tag)
}

func (e *SkippedError) IsSkipped() bool { return true }
