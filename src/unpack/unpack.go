// Package unpack extracts archives into a directory. Every entry lands inside it:
// paths can't climb out, symlinks must be relative and point inside, nothing is written
// through a symlink, and modes keep only permission bits.
package unpack

import (
	"archive/tar"
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Tar extracts a tar stream into destDir, dropping the first strip path components of
// every entry. Directories, regular files and symlinks are extracted; other entry types
// are skipped.
func Tar(r io.Reader, destDir string, strip int) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		parts := strings.Split(hdr.Name, "/")
		if len(parts) <= strip {
			continue // the stripped prefix itself
		}
		rel := strings.Join(parts[strip:], "/")
		if rel == "" {
			continue
		}
		var werr error
		switch hdr.Typeflag {
		case tar.TypeDir:
			werr = writeDir(destDir, rel)
		case tar.TypeReg:
			werr = writeFile(destDir, rel, tr, os.FileMode(hdr.Mode))
		case tar.TypeSymlink:
			werr = writeSymlink(destDir, rel, hdr.Linkname)
		}
		if werr != nil {
			return fmt.Errorf("archive entry %s: %w", hdr.Name, werr)
		}
	}
}

// Zip extracts the zip archive at path into destDir.
func Zip(path, destDir string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if err := zipEntry(zf, destDir); err != nil {
			return fmt.Errorf("archive entry %s: %w", zf.Name, err)
		}
	}
	return nil
}

func zipEntry(zf *zip.File, destDir string) error {
	mode := zf.Mode()
	if mode.IsDir() {
		return writeDir(destDir, zf.Name)
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if mode&os.ModeSymlink != 0 {
		link, err := io.ReadAll(io.LimitReader(rc, 4096))
		if err != nil {
			return err
		}
		return writeSymlink(destDir, zf.Name, string(link))
	}
	if !mode.IsRegular() {
		return nil
	}
	return writeFile(destDir, zf.Name, rc, mode)
}

// target resolves rel to a path strictly inside destDir, refusing one that leaves it
// or whose parent directories include a symlink.
func target(destDir, rel string) (string, error) {
	root := filepath.Clean(destDir)
	t := filepath.Join(root, rel)
	if !strings.HasPrefix(t, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes %s", destDir)
	}
	if err := noSymlinkBetween(root, filepath.Dir(t)); err != nil {
		return "", err
	}
	return t, nil
}

func writeDir(destDir, rel string) error {
	if filepath.Join(destDir, rel) == filepath.Clean(destDir) {
		return os.MkdirAll(destDir, 0o755) // the archive's own root
	}
	t, err := target(destDir, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(t, 0o755)
}

func writeFile(destDir, rel string, r io.Reader, mode os.FileMode) error {
	t, err := target(destDir, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(t); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(t); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(t, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil { //nolint:gosec // callers verify the archive or produced it
		f.Close()
		return err
	}
	return f.Close()
}

func writeSymlink(destDir, rel, link string) error {
	t, err := target(destDir, rel)
	if err != nil {
		return err
	}
	root := filepath.Clean(destDir) + string(os.PathSeparator)
	if filepath.IsAbs(link) || !strings.HasPrefix(filepath.Join(filepath.Dir(t), link), root) {
		return fmt.Errorf("symlink to %s escapes %s", link, destDir)
	}
	if err := os.MkdirAll(filepath.Dir(t), 0o755); err != nil {
		return err
	}
	_ = os.Remove(t)
	return os.Symlink(link, t)
}

// noSymlinkBetween reports an error when any directory from root down to dir is a
// symlink; directories not yet created are fine.
func noSymlinkBetween(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return err
	}
	cur := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("writes through symlink %s", cur)
		}
	}
	return nil
}
