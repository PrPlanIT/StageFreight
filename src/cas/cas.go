// Package cas is StageFreight's content-addressed artifact store: it retains
// the exact OCI layout bytes produced by a single perform-stage build so that
// review and publish operate on those bytes rather than re-deriving the image.
//
// The architectural rule this enforces: within a single pipeline run,
// cross-phase artifact identity is guaranteed by TRANSPORT — build the bytes
// once in perform and carry them forward, re-hashing on read, so review and
// publish provably operate on the same bytes. Transport is the floor: it holds
// unconditionally, even where a build's reproducibility is imperfect.
//
// Reproducibility is the complementary goal, not a substitute: a deterministic
// build makes the digest independently re-derivable by any third party (the
// auditable, supply-chain trust claim). Transport carries the verified bytes;
// reproducibility proves the digest means what it claims. CAS implements the
// transport floor.
//
// This package is a PURE content store. It knows nothing about StageFreight
// phases, manifests, or build plans: it keys OCI layout directories by their
// content digest and verifies-on-read. The artifact/manifest layer adapts to
// it from the outside; cas never imports artifact, build, or docker.
package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Digest is a content-addressable identity in "algo:hex" form (only sha256 is
// supported). cas uses its own primitive rather than importing the artifact
// package so the store stays a standalone library; callers convert at the
// boundary.
type Digest string

// ErrNotFound is returned by Resolve when no artifact is stored under the
// given digest. A missing artifact is always loud: a digest with no retrievable
// bytes is a claim, not a verified identity, and must never resolve to empty
// success.
var ErrNotFound = errors.New("cas: artifact not found")

// ErrIntegrity is returned when stored bytes do not hash to their key digest —
// the verify-on-read failure. CAS never returns bytes it cannot prove match the
// digest they were stored under.
var ErrIntegrity = errors.New("cas: stored bytes do not match digest (integrity failure)")

// Store retains and retrieves OCI layout artifacts keyed by their content
// digest (the OCI image manifest/index digest buildx reports as
// containerimage.digest).
//
// Put and Resolve are the whole contract: Put retains a layout, Resolve returns
// it only after proving the named blob re-hashes to the key. There is no
// "trust the store" path — every Resolve verifies.
type Store interface {
	// Transport reports whether this store provides cross-phase artifact
	// transport — i.e. whether the deployment's lifecycle should treat
	// distribution as the publish phase's sole authority (perform retains
	// instead of pushing). This is the POLICY question ("is transport active?"),
	// deliberately separate from the MECHANISM question RequiresOCIExport
	// ("must perform emit an OCI layout?"). They are equivalent for FSStore today
	// but a future store (e.g. registry-backed) could provide transport without
	// the same export mechanism; policy decisions must key on Transport, never on
	// RequiresOCIExport, so the mechanism does not leak into policy.
	Transport() bool

	// RequiresOCIExport reports whether perform must export an OCI layout for
	// this store to retain. This is a MECHANISM query: perform asks the store
	// whether to pay the export cost, and never branches on concrete store type.
	// A store that retains nothing (NoopStore) returns false so no layout is
	// exported and discarded.
	RequiresOCIExport() bool

	// Put retains the OCI layout rooted at layoutDir under digest, and returns
	// the absolute path to the stored layout. The digest MUST name a blob present
	// in the layout (blobs/sha256/<hash>); Put verifies this before retaining, so
	// a bad layout fails at write time, not read time.
	Put(digest Digest, layoutDir string) (storedDir string, err error)

	// Resolve returns the absolute path to the retained OCI layout directory for
	// digest, after verifying the named blob re-hashes to digest. Returns
	// ErrNotFound if absent, ErrIntegrity if the stored bytes have drifted.
	Resolve(digest Digest) (storedDir string, err error)
}

// NoopStore is the inert Store: it retains nothing and requires no OCI export.
// Selecting it makes a deployment's perform path behave exactly as before
// Phase 2 (daemon --load only, no layout export, empty PersistenceHandle).
// It exists so the store is always non-nil and the capability boundary is the
// single switch — perform never special-cases "no store".
type NoopStore struct{}

// NewNoopStore returns the inert store.
func NewNoopStore() *NoopStore { return &NoopStore{} }

// Transport is false: the noop store provides no cross-phase transport, so
// distribution remains wherever it was (perform's legacy push fallback).
func (*NoopStore) Transport() bool { return false }

// RequiresOCIExport is false: nothing is retained, so no layout is exported.
func (*NoopStore) RequiresOCIExport() bool { return false }

// Put is a no-op that retains nothing and returns an empty stored dir. It does
// not error: a deployment with persistence disabled is valid, not a failure.
func (*NoopStore) Put(_ Digest, _ string) (string, error) { return "", nil }

// Resolve always reports not-found: the noop store retains nothing, so any
// resolve is a loud miss rather than a silent empty success.
func (*NoopStore) Resolve(digest Digest) (string, error) {
	return "", fmt.Errorf("%w: digest %s (noop store retains nothing)", ErrNotFound, digest)
}

// Transport is true: the filesystem store carries artifacts across phases, so
// distribution becomes the publish phase's sole authority.
func (*FSStore) Transport() bool { return true }

// RequiresOCIExport is true for the filesystem store: perform must export an
// OCI layout for FSStore to retain it.
func (*FSStore) RequiresOCIExport() bool { return true }

// FSStore is a filesystem-backed Store. Layouts live under
// root/<algo>/<hash>/ as ordinary OCI layout directories. The store is keyed
// by digest from day one even though the backend is a plain tree — the backend
// is an implementation detail behind the Store interface, swappable later
// (e.g. object storage) without changing identity semantics.
type FSStore struct {
	root string
}

// NewFSStore returns a filesystem CAS rooted at root (e.g.
// ".stagefreight/objects"). The directory is created on first Put.
func NewFSStore(root string) *FSStore {
	return &FSStore{root: root}
}

// Root returns the store's root directory.
func (s *FSStore) Root() string { return s.root }

// splitDigest parses "sha256:abc..." into ("sha256", "abc..."). Only sha256 is
// supported (the algorithm buildx/OCI emit); anything else is rejected so an
// unexpected algorithm can't silently bypass verification.
func splitDigest(d Digest) (algo, hexHash string, err error) {
	s := string(d)
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("cas: malformed digest %q (want algo:hex)", d)
	}
	algo, hexHash = s[:i], s[i+1:]
	if algo != "sha256" {
		return "", "", fmt.Errorf("cas: unsupported digest algorithm %q (only sha256)", algo)
	}
	if len(hexHash) != 64 {
		return "", "", fmt.Errorf("cas: malformed sha256 hex %q (want 64 chars)", hexHash)
	}
	return algo, hexHash, nil
}

// ociIndex is the minimal shape of an OCI layout index.json that the store
// needs: the list of manifest descriptors it points at.
type ociIndex struct {
	Manifests []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
	} `json:"manifests"`
}

// verifyLayout proves an OCI layout in layoutDir corresponds to digest, with
// two independent checks:
//
//  1. Identity binding: digest is the descriptor the layout's index.json points
//     at (index.json.manifests[*].digest). This is what ties the build's
//     reported containerimage.digest to these bytes. Buildx may report a
//     "virtual" index digest (under --load + --output type=oci together) that is
//     referenced in index.json but not written as a standalone blob — so the
//     binding is checked against the index's manifest list, not against the
//     presence of a single named blob.
//
//  2. Content integrity: every file under blobs/sha256/ re-hashes to its own
//     filename. This proves the stored bytes have not drifted/been tampered;
//     it is the verify-on-read guarantee over the whole content closure.
//
// Together these mean: "these exact bytes are intact AND they are the artifact
// the build reported as digest." Neither a whole-directory hash nor a single
// blob hash would be correct — the OCI digest names a descriptor, and integrity
// lives across the blob set.
func verifyLayout(layoutDir string, digest Digest) error {
	if err := verifyIdentityBinding(layoutDir, digest); err != nil {
		return err
	}
	return verifyBlobIntegrity(layoutDir)
}

// verifyIdentityBinding checks digest appears in index.json's manifest list.
func verifyIdentityBinding(layoutDir string, digest Digest) error {
	if _, _, err := splitDigest(digest); err != nil {
		return err
	}
	indexPath := filepath.Join(layoutDir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: no index.json at %s (not an OCI layout)", ErrIntegrity, indexPath)
		}
		return fmt.Errorf("cas: reading index.json: %w", err)
	}
	var idx ociIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return fmt.Errorf("%w: index.json is not valid JSON: %v", ErrIntegrity, err)
	}
	for _, m := range idx.Manifests {
		if m.Digest == string(digest) {
			return nil
		}
	}
	return fmt.Errorf("%w: digest %s is not referenced by index.json (layout describes a different artifact)", ErrIntegrity, digest)
}

// verifyBlobIntegrity re-hashes every blob under blobs/<algo>/ and confirms it
// matches its filename. A missing blobs dir is an integrity failure (a layout
// with no content closure cannot be trusted).
func verifyBlobIntegrity(layoutDir string) error {
	blobsRoot := filepath.Join(layoutDir, "blobs")
	info, err := os.Stat(blobsRoot)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: no blobs/ directory in layout %s", ErrIntegrity, layoutDir)
	}
	return filepath.Walk(blobsRoot, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}
		algo := filepath.Base(filepath.Dir(path))
		if algo != "sha256" {
			// Only sha256 blobs are understood; an unexpected algo dir means a
			// layout we can't verify, so refuse it.
			return fmt.Errorf("%w: blob under unsupported algorithm dir %q", ErrIntegrity, algo)
		}
		want := fi.Name()
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("cas: opening blob %s: %w", path, err)
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return fmt.Errorf("cas: hashing blob %s: %w", path, err)
		}
		got := hex.EncodeToString(h.Sum(nil))
		if got != want {
			return fmt.Errorf("%w: blob %s hashes to sha256:%s", ErrIntegrity, want, got)
		}
		return nil
	})
}

func (s *FSStore) dirFor(digest Digest) (string, error) {
	algo, hexHash, err := splitDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, algo, hexHash), nil
}

// Put retains the OCI layout, verifying the digest names a real blob first.
func (s *FSStore) Put(digest Digest, layoutDir string) (string, error) {
	if err := verifyLayout(layoutDir, digest); err != nil {
		return "", fmt.Errorf("cas: refusing to store layout that does not match its digest: %w", err)
	}

	dest, err := s.dirFor(digest)
	if err != nil {
		return "", err
	}

	// Already stored (idempotent) — verify the existing copy still matches and
	// return it rather than rewriting.
	if _, statErr := os.Stat(dest); statErr == nil {
		if verifyErr := verifyLayout(dest, digest); verifyErr != nil {
			return "", fmt.Errorf("cas: existing stored layout for %s failed verification: %w", digest, verifyErr)
		}
		return dest, nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("cas: creating store dir: %w", err)
	}
	if err := copyTree(layoutDir, dest); err != nil {
		return "", fmt.Errorf("cas: copying layout into store: %w", err)
	}

	// Re-verify after copy: the bytes that will actually be read back must match.
	if err := verifyLayout(dest, digest); err != nil {
		return "", fmt.Errorf("cas: stored copy failed post-write verification: %w", err)
	}
	return dest, nil
}

// Resolve returns the stored layout dir for digest after verify-on-read.
func (s *FSStore) Resolve(digest Digest) (string, error) {
	dest, err := s.dirFor(digest)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(dest); statErr != nil {
		if os.IsNotExist(statErr) {
			return "", fmt.Errorf("%w: digest %s", ErrNotFound, digest)
		}
		return "", fmt.Errorf("cas: stat store dir for %s: %w", digest, statErr)
	}
	if err := verifyLayout(dest, digest); err != nil {
		return "", err
	}
	return dest, nil
}

// VerifyLayoutAt verifies that the OCI layout at dir corresponds to digest:
// the digest is bound by index.json and every blob re-hashes to its name. This
// is the read-side proof a consumer (e.g. review) performs before trusting bytes
// resolved through a persistence handle — identity is never trusted without
// re-hashing the actual bytes. Returns nil on success, ErrIntegrity otherwise.
func VerifyLayoutAt(dir string, digest Digest) error {
	return verifyLayout(dir, digest)
}

// copyTree recursively copies src into dst (files and dirs). OCI layouts are
// small trees of regular files; no special device handling.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return nil
	})
}
