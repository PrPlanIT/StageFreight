//go:build unix

package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AcquireInstallLock acquires an exclusive file lock for a tool install directory.
// Blocks up to timeout. Returns the lock file handle (caller must call ReleaseInstallLock).
// If lock cannot be acquired within timeout, returns an error — no deadlock.
func AcquireInstallLock(dir string, timeout time.Duration) (*os.File, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating lock dir %s: %w", dir, err)
	}

	lockPath := filepath.Join(dir, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", lockPath, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			// Lock acquired — write our PID for stale detection
			f.Truncate(0)
			f.Seek(0, 0)
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Sync()
			return f, nil
		}

		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timeout acquiring install lock at %s after %v", lockPath, timeout)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

// ReleaseInstallLock releases the file lock and closes the handle.
func ReleaseInstallLock(f *os.File) {
	if f == nil {
		return
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}
