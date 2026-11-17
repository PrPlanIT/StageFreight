package toolchain

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ulikunitz/xz"

	"github.com/PrPlanIT/StageFreight/src/unpack"
)

// httpClient is the shared HTTP client for toolchain operations.
// Explicit timeouts and user-agent — no bare http.Get.
var httpClient = &http.Client{
	Timeout: 5 * time.Minute,
}

const userAgent = "StageFreight-Toolchain/1"

// httpGet performs a GET request with the StageFreight user-agent and timeout.
func httpGet(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	return httpClient.Do(req)
}

// downloadToTemp fetches a URL to a temporary file and returns its path.
// Caller is responsible for removing the temp file.
func downloadToTemp(url string) (string, error) {
	resp, err := httpGet(url)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "sf-toolchain-*.tar.gz")
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}

// fileSHA256 computes the SHA256 hash of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// installStandaloneBinary copies a downloaded binary to its final path atomically.
// Writes to .tmp, syncs, chmod, then renames. Safe against partial reads and crashes.
func installStandaloneBinary(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
		return err
	}
	return atomicCopyBinary(srcPath, dstPath)
}

// installFromArchive extracts a tar.gz archive to a temp dir, locates the named
// binary, and copies ONLY that binary to the final path atomically.
// If 0 or >1 binaries match, returns an error (ambiguity = hard failure).
func installFromArchive(archivePath, dstBinPath, binaryName string) error {
	return installExtracted(archivePath, dstBinPath, binaryName, func(dir string) error {
		return extractTarGzTree(archivePath, dir, 0)
	})
}

// installFromZip is installFromArchive for a .zip archive.
func installFromZip(archivePath, dstBinPath, binaryName string) error {
	return installExtracted(archivePath, dstBinPath, binaryName, func(dir string) error {
		return unpack.Zip(archivePath, dir)
	})
}

// installExtracted extracts an archive with extract, then installs the one binary named binaryName.
func installExtracted(archivePath, dstBinPath, binaryName string, extract func(dir string) error) error {
	tmpDir, err := os.MkdirTemp("", "sf-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := extract(tmpDir); err != nil {
		return fmt.Errorf("extracting archive: %w", err)
	}

	// Walk to find the binary by name — exact match only
	var matches []string
	filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() && info.Name() == binaryName {
			matches = append(matches, path)
		}
		return nil
	})

	switch len(matches) {
	case 0:
		return fmt.Errorf("binary %q not found in archive", binaryName)
	case 1:
		if err := os.MkdirAll(filepath.Dir(dstBinPath), 0755); err != nil {
			return err
		}
		// Atomic copy (not rename — may be cross-filesystem between tmpdir and cache)
		return atomicCopyBinary(matches[0], dstBinPath)
	default:
		return fmt.Errorf("ambiguous: %d files named %q in archive", len(matches), binaryName)
	}
}

// atomicCopyBinary copies a binary to dstPath atomically via temp file + rename.
// Safe against cross-filesystem boundaries (no os.Rename across mounts).
func atomicCopyBinary(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	// Write to .tmp in the SAME directory as dst (same filesystem = rename safe)
	tmp := dstPath + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return err
	}

	// Sync to disk before rename
	if err := dst.Sync(); err != nil {
		dst.Close()
		os.Remove(tmp)
		return err
	}
	dst.Close()

	return os.Rename(tmp, dstPath)
}

// extractTarGzTree extracts a full directory-tree distribution (e.g. node, whose
// binary needs sibling files: lib/node_modules/npm, and whose bin/npm is a SYMLINK)
// into destDir, stripping the first `strip` leading path components (node's tarball
// has a single top-level node-vX-linux-ARCH/ dir → strip 1 → bin/node lands directly
// under destDir), keeping the whole tree and its in-tree symlinks (see unpack.Tar).
func extractTarGzTree(archivePath, destDir string, strip int) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	return unpack.Tar(gz, destDir, strip)
}

// extractTarXzTree is extractTarGzTree for an xz-compressed tarball (e.g. zig).
func extractTarXzTree(archivePath, destDir string, strip int) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	xzr, err := xz.NewReader(f)
	if err != nil {
		return fmt.Errorf("xz: %w", err)
	}
	return unpack.Tar(xzr, destDir, strip)
}
