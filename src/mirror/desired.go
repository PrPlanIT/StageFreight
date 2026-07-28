package mirror

import (
	"context"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

// releaseSource is the read side of the source forge the desired-set producer
// needs. *forge.Forge satisfies it.
type releaseSource interface {
	ListReleases(ctx context.Context) ([]forge.ReleaseInfo, error)
	ListReleaseAssets(ctx context.Context, releaseID string) ([]forge.ReleaseAsset, error)
}

// BuildDesiredReleases computes a mirror's desired release set: the source's
// releases matching `templates`, filtered by the retention `policy`, each with
// its file assets resolved for re-hosting.
//
// This is the retention-as-desired-set layering — retention decides what SHOULD
// exist; the reconciler (ReconcileReleases) converges the mirror to it. The
// keep decision is read out of the shipped retention engine via a recording
// store (no refactor, no real deletes).
func BuildDesiredReleases(ctx context.Context, src releaseSource, templates []string, policy config.RetentionPolicy) ([]DesiredRelease, error) {
	rels, err := src.ListReleases(ctx)
	if err != nil {
		return nil, err
	}

	patterns := retention.TemplatesToPatterns(templates)
	inScope := func(tag string) bool {
		return len(templates) == 0 || config.MatchPatterns(patterns, tag)
	}

	// Run retention over the in-scope tags via a recording store to learn the
	// PRUNED set (which the mirror should NOT hold).
	items := make([]retention.Item, 0, len(rels))
	for _, r := range rels {
		items = append(items, retention.Item{Name: r.TagName, CreatedAt: r.CreatedAt})
	}
	rec := &keptRecorder{items: items, pruned: map[string]bool{}}
	if _, err := retention.Apply(ctx, rec, templates, policy); err != nil {
		return nil, err
	}

	var desired []DesiredRelease
	for _, r := range rels {
		if !inScope(r.TagName) || rec.pruned[r.TagName] {
			continue // not this channel's release, or retention dropped it
		}
		assets, err := src.ListReleaseAssets(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		d := DesiredRelease{Tag: r.TagName, Name: r.Name, Body: r.Description, Prerelease: r.Prerelease}
		for _, a := range assets {
			d.Assets = append(d.Assets, DesiredAsset{Name: a.Name, Digest: a.Digest, Size: a.Size, Source: a})
		}
		desired = append(desired, d)
	}
	return desired, nil
}

// DesiredFromReleases builds the desired set from an already-selected list of
// source releases (e.g. scoped by a sync facet), resolving each release's file
// assets for re-hosting. Use this when the caller has already decided WHICH
// releases to mirror; BuildDesiredReleases is the template+retention front door.
func DesiredFromReleases(ctx context.Context, src releaseSource, rels []forge.ReleaseInfo) ([]DesiredRelease, error) {
	var desired []DesiredRelease
	for _, r := range rels {
		assets, err := src.ListReleaseAssets(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		d := DesiredRelease{Tag: r.TagName, Name: r.Name, Body: r.Description, Prerelease: r.Prerelease}
		for _, a := range assets {
			d.Assets = append(d.Assets, DesiredAsset{Name: a.Name, Digest: a.Digest, Size: a.Size, Source: a})
		}
		desired = append(desired, d)
	}
	return desired, nil
}

// keptRecorder is a retention.Store over a fixed item list that RECORDS which
// items retention would delete instead of deleting them — letting us read the
// kept set out of the shipped engine without touching it.
type keptRecorder struct {
	items  []retention.Item
	pruned map[string]bool
}

func (s *keptRecorder) List(context.Context) ([]retention.Item, error) { return s.items, nil }
func (s *keptRecorder) Delete(_ context.Context, name string) error {
	s.pruned[name] = true
	return nil
}
