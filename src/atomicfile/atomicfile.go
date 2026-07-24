// Package atomicfile provides atomic file write operations.
//
// All writes follow the same contract: write to a temporary file in the
// same directory as the target, fsync, then rename. This guarantees that
// readers never see a partially-written file — they see the old content
// or the new content, never a torn intermediate state.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes data atomically: tmp → fsync → rename.
// The temporary file is created in the same directory as path to ensure
// the rename is same-filesystem (required for atomic rename on POSIX).
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("atomicfile: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".sf-atomic-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	// Ensure cleanup on any failure path.
	success := false
	defer func() {
		if !success {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("atomicfile: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("atomicfile: fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomicfile: close: %w", err)
	}

	// Set permissions before rename so the file is correct on arrival.
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("atomicfile: chmod: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomicfile: rename %s → %s: %w", tmpPath, path, err)
	}

	// Fsync the parent directory to ensure the rename is durable.
	// Without this, a power loss after rename can lose the directory entry
	// on some filesystems (ext4 without journal, XFS in certain modes).
	if dirFd, err := os.Open(dir); err == nil {
		dirFd.Sync()
		dirFd.Close()
	}

	success = true
	return nil
}
