package paths

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// CacheLockName is the flock(2) file at a Cache bucket root, shared by every StageFreight
// run on a host. A process that resolves the root holds it shared for its lifetime;
// eviction takes it exclusively without waiting, and evicts only cold entries when it can't.
const CacheLockName = ".lock"

var cacheLocks = struct {
	sync.Mutex
	held map[string]*os.File // canonical root → the fd carrying this process's shared lock
}{held: map[string]*os.File{}}

// lockKey is root's canonical spelling, so two spellings of one root share one lock fd.
func lockKey(root string) string {
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if r, err := filepath.Abs(root); err == nil {
		root = r
	}
	return filepath.Clean(root)
}

// openLock opens root's lock file read-only (flock needs no write access), creating it
// world-readable so runs as any user can lock it.
func openLock(root string) (*os.File, error) {
	return os.OpenFile(filepath.Join(root, CacheLockName), os.O_CREATE|os.O_RDONLY, 0o644)
}

// HoldCacheRoot takes a shared lock on root for the rest of this process's life, waiting
// out a running eviction. Idempotent per root; a root that can't carry the lock file is
// not locked.
func HoldCacheRoot(root string) {
	if root == "" {
		return
	}
	key := lockKey(root)
	cacheLocks.Lock()
	held := cacheLocks.held[key] != nil
	cacheLocks.Unlock()
	if held {
		return
	}
	f, err := openLock(key)
	if err != nil {
		return
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return
	}
	cacheLocks.Lock()
	defer cacheLocks.Unlock()
	if cacheLocks.held[key] != nil {
		f.Close()
		return
	}
	cacheLocks.held[key] = f
}

// ExclusiveCacheRoot takes root exclusively for eviction, without waiting; ok is false
// when any other process holds it. release restores this process's shared hold, if any.
func ExclusiveCacheRoot(root string) (release func(), ok bool) {
	noop := func() {}
	if root == "" {
		return noop, false
	}
	key := lockKey(root)
	cacheLocks.Lock()
	defer cacheLocks.Unlock()

	f, own := cacheLocks.held[key], true
	if f == nil {
		own = false
		var err error
		if f, err = openLock(key); err != nil {
			return noop, false
		}
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// Converting a held lock is not atomic: a failed upgrade may have dropped our
		// shared lock, so take it back before reporting the root busy.
		if own {
			_ = syscall.Flock(fd, syscall.LOCK_SH)
		} else {
			f.Close()
		}
		return noop, false
	}
	return func() {
		cacheLocks.Lock()
		defer cacheLocks.Unlock()
		if own {
			_ = syscall.Flock(fd, syscall.LOCK_SH)
			return
		}
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		f.Close()
	}, true
}
