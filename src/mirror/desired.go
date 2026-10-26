package mirror

import (
	"context"
	"fmt"

	"github.com/PrPlanIT/StageFreight/src/forge"
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

// DesiredFromReleases builds the desired set from an already-selected list of
// source releases (e.g. scoped by a sync facet), resolving each release's file
// assets for re-hosting. The caller decides WHICH releases to mirror (sync.ScopedReleases).
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
