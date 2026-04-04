// Package atomicfile provides atomic file write operations.
//
// All writes follow the same contract: write to a temporary file in the
// same directory as the target, fsync, then rename. This guarantees that
// readers never see a partially-written file — they see the old content
// or the new content, never a torn intermediate state.
package atomicfile

import (
	"crypto/sha256"
	"encoding/hex"
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

	success = true
	return nil
}

// WriteVerifiedPair writes a file and its SHA-256 checksum sidecar atomically.
// Both files are written to temporaries, fsynced, then renamed in order
// (data first, then checksum). The checksum sidecar uses the standard format:
//
//	<hex>  <basename>\n
//
// If the data file rename succeeds but the checksum rename fails, the data
// file is left in place (it's valid) and the error is returned so callers
// can detect the inconsistency.
func WriteVerifiedPair(path string, data []byte, perm os.FileMode) error {
	// Compute checksum.
	hash := sha256.Sum256(data)
	checksumHex := hex.EncodeToString(hash[:])
	checksumContent := []byte(checksumHex + "  " + filepath.Base(path) + "\n")
	checksumPath := path + ".sha256"

	// Write data file atomically.
	if err := WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("atomicfile: data: %w", err)
	}

	// Write checksum sidecar atomically.
	if err := WriteFile(checksumPath, checksumContent, perm); err != nil {
		return fmt.Errorf("atomicfile: checksum sidecar: %w", err)
	}

	return nil
}
