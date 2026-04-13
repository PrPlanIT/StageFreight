package toolchain

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	tmpDir, err := os.MkdirTemp("", "sf-extract-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := extractTarGz(archivePath, tmpDir); err != nil {
		return fmt.Errorf("extracting archive: %w", err)
	}

	// Walk to find the binary by name — exact match only
	var matches []string
	filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == binaryName {
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

// extractTarGz extracts all entries from a tar.gz archive into destDir.
func extractTarGz(archivePath, destDir string) error {
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

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		target := filepath.Join(destDir, hdr.Name)

		// Path traversal guard
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("path traversal in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outf, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outf, tr); err != nil {
				outf.Close()
				return err
			}
			outf.Close()
		case tar.TypeSymlink:
			// Symlinks ignored — we only need the binary, and symlinks
			// in toolchain archives are an unnecessary attack surface.
			continue
		}
	}
	return nil
}

// extractGoArchive extracts the Go distribution tarball into destDir.
// The Go tarball has a top-level `go/` directory. We extract the full
// distribution (bin, pkg, src — all needed for go run/build).
func extractGoArchive(archivePath, destDir string) error {
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

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		// Go tarball has go/ prefix — strip it
		name := hdr.Name
		if !strings.HasPrefix(name, "go/") {
			continue
		}
		relPath := strings.TrimPrefix(name, "go/")
		if relPath == "" {
			continue
		}

		target := filepath.Join(destDir, relPath)

		// Path traversal guard
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("path traversal in archive: %s", name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			outf, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outf, tr); err != nil {
				outf.Close()
				return err
			}
			outf.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}

	return nil
}
