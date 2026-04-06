package sync

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// ReleaseData holds all data needed to project a release to an accessory.
type ReleaseData struct {
	Tag         string
	Name        string
	Description string // markdown body
	Draft       bool
	Prerelease  bool
	Assets      []forge.Asset
	Links       []forge.ReleaseLink
}

// SyncRelease projects a release to an accessory forge.
// Tag is the identity key — if a release for this tag already exists,
// it is not recreated (idempotent). Assets are uploaded if missing.
//
// Artifact sync must never mutate repository content (files, refs).
// Only forge-native surfaces.
func SyncRelease(ctx context.Context, accessory config.ResolvedRepo, data ReleaseData) *ReleaseResult {
	result := &ReleaseResult{
		AccessoryID: accessory.ID,
	}

	client, err := forge.NewFromAccessory(
		accessory.Provider,
		accessory.BaseURL,
		accessory.Project,
		accessory.Credentials,
	)
	if err != nil {
		result.Status = SyncFailed
		result.Message = fmt.Sprintf("forge client: %v", err)
		return result
	}

	// Check if release already exists (tag is the identity key)
	var relID string
	existing, _ := findExistingRelease(ctx, client, data.Tag)

	if existing != "" {
		// Release exists — no update needed for body (mirror already pushed the tag)
		relID = existing
	} else {
		rel, err := client.CreateRelease(ctx, forge.ReleaseOptions{
			TagName:     data.Tag,
			Name:        data.Name,
			Description: data.Description,
			Draft:       data.Draft,
			Prerelease:  data.Prerelease,
		})
		if err != nil {
			result.Status = SyncFailed
			result.Message = fmt.Sprintf("create release: %v", err)
			return result
		}
		relID = rel.ID
	}

	// Upload assets (skip if release pre-existed — assets likely already there)
	if existing == "" {
		for _, asset := range data.Assets {
			if err := client.UploadAsset(ctx, relID, asset); err != nil {
				// Non-fatal — log and continue
				result.Message += fmt.Sprintf("; asset %s: %v", filepath.Base(asset.FilePath), err)
			}
		}

		// Add links where supported
		for _, link := range data.Links {
			if err := client.AddReleaseLink(ctx, relID, link); err != nil {
				// Non-fatal
				result.Message += fmt.Sprintf("; link %s: %v", link.Name, err)
			}
		}
	}

	result.Status = SyncSuccess
	if result.Message == "" {
		result.Message = fmt.Sprintf("release %s projected to %s", data.Tag, accessory.ID)
	} else {
		// Had partial failures but release was created
		result.Message = fmt.Sprintf("release %s projected to %s (with warnings%s)", data.Tag, accessory.ID, result.Message)
	}
	return result
}

// findExistingRelease checks if a release for the given tag already exists.
// Returns the release ID if found, empty string otherwise.
func findExistingRelease(ctx context.Context, client forge.Forge, tag string) (string, error) {
	releases, err := client.ListReleases(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range releases {
		if r.TagName == tag {
			return r.ID, nil
		}
	}
	return "", nil
}
