package mirror

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
	"github.com/PrPlanIT/StageFreight/src/retention"
)

// classifyAssets splits a source release's assets into the three buckets of the mirror
// asset model: downloadable FILES (re-hosted via download→upload), external LINKS
// (registry/image references re-created via AddReleaseLink, never downloaded), and
// DIAGNOSTICS for anything that is neither (disclosed, never uploaded as a pretend-file
// nor silently 404-ed on download). Classification is carried on ReleaseAsset.External /
// .LinkType, derived by the source forge from GitLab's link_type + host comparison.
func classifyAssets(assets []forge.ReleaseAsset) (files []DesiredAsset, links []DesiredLink, diags []string) {
	for _, a := range assets {
		switch {
		case a.External:
			if a.URL == "" {
				diags = append(diags, fmt.Sprintf("asset %q: external reference with no URL — cannot mirror as a link", a.Name))
				continue
			}
			links = append(links, DesiredLink{Name: a.Name, URL: a.URL, LinkType: a.LinkType})
		case a.URL != "":
			files = append(files, DesiredAsset{Name: a.Name, Digest: a.Digest, Size: a.Size, Source: a})
		default:
			diags = append(diags, fmt.Sprintf("asset %q: neither a downloadable file nor an external link — skipped", a.Name))
		}
	}
	return files, links, diags
}

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
		d.Assets, d.Links, d.Diagnostics = classifyAssets(assets)
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
		d.Assets, d.Links, d.Diagnostics = classifyAssets(assets)
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
