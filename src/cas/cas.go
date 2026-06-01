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

// verifyNamedBlob proves that blobs/<algo>/<hex> inside layoutDir hashes to the
// digest. This is the identity check: the digest names a real blob in the
// layout (the manifest/index buildx reported), and that blob's bytes must hash
// to it. NOT a hash of the whole directory or a tarball — that would never
// match the OCI digest.
func verifyNamedBlob(layoutDir string, digest Digest) error {
	algo, hexHash, err := splitDigest(digest)
	if err != nil {
		return err
	}
	blobPath := filepath.Join(layoutDir, "blobs", algo, hexHash)
	f, err := os.Open(blobPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: digest %s names no blob at %s", ErrIntegrity, digest, blobPath)
		}
		return fmt.Errorf("cas: opening blob for %s: %w", digest, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("cas: hashing blob for %s: %w", digest, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != hexHash {
		return fmt.Errorf("%w: blob for %s hashes to sha256:%s", ErrIntegrity, digest, got)
	}
	return nil
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
	if err := verifyNamedBlob(layoutDir, digest); err != nil {
		return "", fmt.Errorf("cas: refusing to store layout that does not match its digest: %w", err)
	}

	dest, err := s.dirFor(digest)
	if err != nil {
		return "", err
	}

	// Already stored (idempotent) — verify the existing copy still matches and
	// return it rather than rewriting.
	if _, statErr := os.Stat(dest); statErr == nil {
		if verifyErr := verifyNamedBlob(dest, digest); verifyErr != nil {
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
	if err := verifyNamedBlob(dest, digest); err != nil {
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
	if err := verifyNamedBlob(dest, digest); err != nil {
		return "", err
	}
	return dest, nil
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
