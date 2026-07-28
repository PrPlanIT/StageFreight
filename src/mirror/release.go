// Package mirror converges a mirror forge toward a desired release set,
// granularly and provenance-bounded: it only ever creates/updates/deletes
// releases IT placed there (marked), never foreign or human-authored ones.
//
// The desired set is computed upstream (typically primary's releases filtered
// by the retention engine); this package merely converges reality to it. Cadence
// is a caller concern — ReconcileReleases is idempotent, so any trigger (publish,
// cron, serve, manual) is equivalent.
package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/forge"
)

// releaseForge is the subset of forge.Forge the release reconciler needs. A
// narrow interface keeps the engine testable with a small fake. *forge.Forge
// implementations satisfy it.
type releaseForge interface {
	ListReleases(ctx context.Context) ([]forge.ReleaseInfo, error)
	CreateRelease(ctx context.Context, opts forge.ReleaseOptions) (*forge.Release, error)
	DeleteRelease(ctx context.Context, tagName string) error
	UploadAsset(ctx context.Context, releaseID string, asset forge.Asset) error
	ListReleaseAssets(ctx context.Context, releaseID string) ([]forge.ReleaseAsset, error)
	DownloadReleaseAsset(ctx context.Context, asset forge.ReleaseAsset) (io.ReadCloser, error)
	DeleteReleaseAsset(ctx context.Context, releaseID, assetID string) error
	UpdateReleaseNotes(ctx context.Context, releaseID, body string) error
}

// DesiredRelease is one release the mirror should hold. Body is verbatim from
// the source; Assets are the file assets to re-host. Fingerprint, if set, is the
// source's content fingerprint (else it's computed).
type DesiredRelease struct {
	Tag         string
	Ref         string
	Name        string
	Body        string
	Prerelease  bool
	Assets      []DesiredAsset
	Fingerprint string
}

// DesiredAsset is a file the release should carry. For MIRRORING, Source names
// how to fetch it from the source forge (re-host). For AUTHORING (upsert), Local
// is a path to the freshly-built file; Local takes precedence over Source.
type DesiredAsset struct {
	Name   string
	Digest string // content digest, "" if unknown
	Size   int64
	Source forge.ReleaseAsset // remote source (mirror re-host)
	Local  string             // local file path (author upsert)
}

// Options tunes convergence. Prune enables removal of OUR mirror releases no
// longer in the desired set (never foreign). InScope, when set, further limits
// pruning to tags it returns true for (the ownership boundary).
type Options struct {
	Prune   bool
	InScope func(tag string) bool
}

// Result reports what the reconcile did (all counts/lists are mirror-side).
type Result struct {
	Created        []string
	Updated        []string
	Pruned         []string
	InSync         int      // skipped via fingerprint fast-path
	SkippedForeign []string // in-scope tag exists but is not ours — left untouched
	Errors         []error
}

// ── provenance marker ────────────────────────────────────────────────────
// SF wraps the content it owns in a marked block; the start marker carries the
// content fingerprint. Presence of the marker = "this release is ours". Human
// prose OUTSIDE the block is preserved across updates.

const markerPrefix = "<!-- sf:mirror fp="
const markerOpenEnd = " -->"
const markerClose = "<!-- /sf:mirror -->"

func openMarker(fp string) string { return markerPrefix + fp + markerOpenEnd }

// wrapManaged builds a full body from scratch: the fingerprinted block only.
func wrapManaged(body, fp string) string {
	return openMarker(fp) + "\n" + body + "\n" + markerClose
}

// readMarker returns the stored fingerprint and whether the body is ours.
func readMarker(body string) (fp string, ours bool) {
	i := strings.Index(body, markerPrefix)
	if i < 0 {
		return "", false
	}
	rest := body[i+len(markerPrefix):]
	j := strings.Index(rest, markerOpenEnd)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[:j]), true
}

// replaceManaged rewrites the managed block in-place (new inner body + fp),
// preserving any human prose before the open marker and after the close marker.
// If markers are absent, the whole body becomes a fresh managed block.
func replaceManaged(existing, body, fp string) string {
	start := strings.Index(existing, markerPrefix)
	if start < 0 {
		return wrapManaged(body, fp)
	}
	endIdx := strings.Index(existing, markerClose)
	if endIdx < 0 {
		return wrapManaged(body, fp)
	}
	before := existing[:start]
	after := existing[endIdx+len(markerClose):]
	return before + wrapManaged(body, fp) + after
}

// fingerprint hashes the body + each asset's identity (digest, or name+size as a
// fallback). Deterministic (assets sorted by name). Forge-independent.
func fingerprint(body string, assets []DesiredAsset) string {
	h := sha256.New()
	h.Write([]byte(body))
	sorted := append([]DesiredAsset{}, assets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, a := range sorted {
		id := a.Digest
		if id == "" {
			id = fmt.Sprintf("size:%d", a.Size)
		}
		h.Write([]byte("\x00" + a.Name + "\x00" + id))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func relType(pre bool) forge.ReleaseType {
	if pre {
		return forge.ReleaseTypePrerelease
	}
	return forge.ReleaseTypeLatest
}

// ReconcileReleases converges dst toward desired, re-hosting assets from src.
// Idempotent; provenance-bounded; granular. Errors on individual releases are
// collected, not fatal — the pass continues.
func ReconcileReleases(ctx context.Context, src, dst releaseForge, desired []DesiredRelease, opts Options) (*Result, error) {
	res := &Result{}

	current, err := dst.ListReleases(ctx)
	if err != nil {
		return res, fmt.Errorf("reconcile: listing mirror releases: %w", err)
	}
	byTag := make(map[string]forge.ReleaseInfo, len(current))
	for _, m := range current {
		byTag[m.TagName] = m
	}
	desiredTags := make(map[string]bool, len(desired))

	for _, d := range desired {
		desiredTags[d.Tag] = true
		fp := d.Fingerprint
		if fp == "" {
			fp = fingerprint(d.Body, d.Assets)
		}

		m, exists := byTag[d.Tag]
		if !exists {
			if err := createOnMirror(ctx, src, dst, d, fp); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("create %s: %w", d.Tag, err))
			} else {
				res.Created = append(res.Created, d.Tag)
			}
			continue
		}

		// Exists — provenance gate: never touch a release we didn't place.
		stored, ours := readMarker(m.Description)
		if !ours {
			res.SkippedForeign = append(res.SkippedForeign, d.Tag)
			continue
		}
		if stored == fp {
			res.InSync++ // fingerprint fast-path: already converged
			continue
		}
		if err := updateOnMirror(ctx, src, dst, d, m, fp); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("update %s: %w", d.Tag, err))
		} else {
			res.Updated = append(res.Updated, d.Tag)
		}
	}

	if opts.Prune {
		for _, m := range current {
			if desiredTags[m.TagName] {
				continue
			}
			if _, ours := readMarker(m.Description); !ours {
				continue // never prune foreign
			}
			if opts.InScope != nil && !opts.InScope(m.TagName) {
				continue // outside the ownership boundary
			}
			if err := dst.DeleteRelease(ctx, m.TagName); err != nil {
				res.Errors = append(res.Errors, fmt.Errorf("prune %s: %w", m.TagName, err))
			} else {
				res.Pruned = append(res.Pruned, m.TagName)
			}
		}
	}

	return res, nil
}

func createOnMirror(ctx context.Context, src, dst releaseForge, d DesiredRelease, fp string) error {
	rel, err := dst.CreateRelease(ctx, forge.ReleaseOptions{
		TagName:     d.Tag,
		Ref:         d.Ref,
		Name:        d.Name,
		Description: wrapManaged(d.Body, fp),
		Type:        relType(d.Prerelease),
	})
	if err != nil {
		return err
	}
	return reconcileAssets(ctx, src, dst, rel.ID, d.Assets)
}

func updateOnMirror(ctx context.Context, src, dst releaseForge, d DesiredRelease, m forge.ReleaseInfo, fp string) error {
	newBody := replaceManaged(m.Description, d.Body, fp)
	if newBody != m.Description {
		if err := dst.UpdateReleaseNotes(ctx, m.ID, newBody); err != nil {
			return err
		}
	}
	return reconcileAssets(ctx, src, dst, m.ID, d.Assets)
}

// reconcileAssets converges the mirror release's file assets granularly: it
// uploads a missing asset, replaces a drifted one (delete+re-host), and leaves
// unchanged ones untouched.
func reconcileAssets(ctx context.Context, src, dst releaseForge, releaseID string, want []DesiredAsset) error {
	have, err := dst.ListReleaseAssets(ctx, releaseID)
	if err != nil {
		return err
	}
	haveByName := make(map[string]forge.ReleaseAsset, len(have))
	for _, a := range have {
		haveByName[a.Name] = a
	}
	for _, w := range want {
		existing, present := haveByName[w.Name]
		if present && assetMatches(existing, w) {
			continue // unchanged
		}
		if present {
			if err := dst.DeleteReleaseAsset(ctx, releaseID, existing.ID); err != nil {
				return fmt.Errorf("replace asset %s: %w", w.Name, err)
			}
		}
		if err := rehost(ctx, src, dst, releaseID, w); err != nil {
			return err
		}
	}
	return nil
}

// assetMatches compares by digest when both sides expose one, else by size.
func assetMatches(have forge.ReleaseAsset, want DesiredAsset) bool {
	if have.Digest != "" && want.Digest != "" {
		return have.Digest == want.Digest
	}
	return have.Size == want.Size && want.Size != 0
}

// rehost places an asset onto dst. For an author upsert (a.Local set) it uploads
// the local file directly; for a mirror re-host it streams from src into a temp
// file first. src may be nil when every asset is local.
func rehost(ctx context.Context, src, dst releaseForge, releaseID string, a DesiredAsset) error {
	if a.Local != "" {
		if err := dst.UploadAsset(ctx, releaseID, forge.Asset{Name: a.Name, FilePath: a.Local}); err != nil {
			return fmt.Errorf("upload %s: %w", a.Name, err)
		}
		return nil
	}
	if src == nil {
		return fmt.Errorf("asset %s: no local file and no source forge", a.Name)
	}

	rc, err := src.DownloadReleaseAsset(ctx, a.Source)
	if err != nil {
		return fmt.Errorf("download %s: %w", a.Name, err)
	}
	defer rc.Close()

	tmp, err := os.CreateTemp("", "sf-rehost-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		return fmt.Errorf("stream %s: %w", a.Name, err)
	}
	tmp.Close()

	if err := dst.UploadAsset(ctx, releaseID, forge.Asset{Name: a.Name, FilePath: tmpPath}); err != nil {
		return fmt.Errorf("upload %s: %w", a.Name, err)
	}
	return nil
}
