// Package promote distributes a content-store OCI layout to a registry WITHOUT
// rebuilding and WITHOUT a daemon round-trip, preserving the exact index digest
// that perform recorded and review verified.
//
// This is the trust-chain's final link: publish must distribute the same bytes
// (digest D) that review approved. The daemon path (docker load → push) is
// disqualified because the daemon collapses the OCI index and re-addresses it,
// producing a DIFFERENT digest D′ — silently breaking "review approves X,
// publish distributes X". go-containerregistry writes the layout's manifests
// and blobs straight to the registry over the OCI distribution protocol, so the
// index digest is preserved (verified empirically: layout digest == registry
// digest).
package promote

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Result reports what a promotion published.
type Result struct {
	Ref    string // fully-qualified pushed reference (registry/path:tag)
	Digest string // the index digest served by the registry after push
}

// LayoutToRegistry pushes the OCI layout at layoutDir to ref (e.g.
// "docker.io/org/app:v1"), preserving wantDigest. It returns an error if the
// layout's index digest does not equal wantDigest before push (refusing to
// distribute bytes whose identity does not match what was recorded/reviewed) or
// if the registry serves a different digest after push (catching any transport
// transformation). Auth comes from the ambient keychain (docker config), with
// an optional explicit override.
//
// wantDigest is the artifact.Digest recorded in outputs.json — the identity
// review approved. This function is the point where "publish distributes
// exactly digest D" is enforced, not assumed.
func LayoutToRegistry(ctx context.Context, layoutDir, ref, wantDigest string, auth authn.Authenticator) (Result, error) {
	p, err := layout.FromPath(layoutDir)
	if err != nil {
		return Result{}, fmt.Errorf("promote: opening OCI layout %s: %w", layoutDir, err)
	}
	idx, err := p.ImageIndex()
	if err != nil {
		return Result{}, fmt.Errorf("promote: reading layout index: %w", err)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return Result{}, fmt.Errorf("promote: reading index manifest: %w", err)
	}

	// wantDigest (Artifact.Digest = buildx containerimage.digest) is the digest
	// of an entry INSIDE the layout's index — the image (or sub-index) manifest,
	// NOT the wrapping index.json's own digest. Locate that entry and push IT, so
	// the registry serves exactly the digest perform recorded and review verified.
	// This is the identity-preservation guard: we distribute the reviewed
	// artifact, not the layout wrapper around it.
	want, err := v1.NewHash(wantDigest)
	if err != nil {
		return Result{}, fmt.Errorf("promote: parsing recorded digest %q: %w", wantDigest, err)
	}
	var found bool
	for _, desc := range im.Manifests {
		if desc.Digest == want {
			found = true
			break
		}
	}
	if !found {
		return Result{}, fmt.Errorf("promote: recorded digest %s is not an entry in the layout index — refusing to distribute a different artifact than was reviewed", wantDigest)
	}

	tag, err := name.NewTag(ref)
	if err != nil {
		return Result{}, fmt.Errorf("promote: parsing reference %q: %w", ref, err)
	}

	opts := []remote.Option{remote.WithContext(ctx)}
	if auth != nil {
		opts = append(opts, remote.WithAuth(auth))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// Push the recorded entry. Try it as an image first (the common single-image
	// case); fall back to a sub-index for multi-platform entries. Either way the
	// registry ends up serving `want`.
	if img, imgErr := idx.Image(want); imgErr == nil {
		if err := remote.Write(tag, img, opts...); err != nil {
			return Result{}, fmt.Errorf("promote: pushing image %s to %s: %w", want, ref, err)
		}
	} else if subIdx, idxErr := idx.ImageIndex(want); idxErr == nil {
		if err := remote.WriteIndex(tag, subIdx, opts...); err != nil {
			return Result{}, fmt.Errorf("promote: pushing index %s to %s: %w", want, ref, err)
		}
	} else {
		return Result{}, fmt.Errorf("promote: recorded digest %s is neither an image nor an index in the layout: %v / %v", want, imgErr, idxErr)
	}

	// Post-push verification: the registry must serve exactly `want`. Any
	// transport transformation surfaces here, before publish claims success.
	gotDesc, err := remote.Get(tag, opts...)
	if err != nil {
		return Result{}, fmt.Errorf("promote: re-reading pushed artifact from %s: %w", ref, err)
	}
	if gotDesc.Digest != want {
		return Result{}, fmt.Errorf("promote: registry served digest %s but recorded was %s — distribution transformed the artifact", gotDesc.Digest, want)
	}

	return Result{Ref: tag.String(), Digest: want.String()}, nil
}
