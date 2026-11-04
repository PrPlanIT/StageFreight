//go:build unix

package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// UnitLeaseName is the flock(2) file in a cache unit — a directory one tool owns whole
// (a toolchain version, a module cache, a scanner DB). A run holds it shared for its
// lifetime from first use; eviction takes it exclusively without waiting, so a unit in
// use is never removed. Its mtime is the unit's last use.
const UnitLeaseName = ".lease"

// installLockName is the flock(2) file held exclusively while a unit is installed into.
const installLockName = ".lock"

// tombstoneMark names a unit renamed aside for deletion.
const tombstoneMark = ".evicted-"

var leases = struct {
	sync.Mutex
	held map[string]*os.File // canonical unit dir → this process's shared lease
}{held: map[string]*os.File{}}

// CacheUnit is <cache root>/cache/<sub...>, created and leased — or "" when no cache
// root resolves or the unit can't be leased.
func CacheUnit(sub ...string) string {
	root := ResolveCacheRoot("")
	if root == "" {
		return ""
	}
	dir := Cache(root, append([]string{"cache"}, sub...)...)
	if LeaseUnit(dir) != nil {
		return ""
	}
	return dir
}

// LeaseUnit creates dir if needed, holds it shared for the rest of this process, and
// records the use. Idempotent per dir; waits out an eviction in progress.
func LeaseUnit(dir string) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		key := lockKey(dir)
		leases.Lock()
		f := leases.held[key]
		leases.Unlock()
		if f != nil {
			if sameFile(f, filepath.Join(key, UnitLeaseName)) {
				touchNow(f.Name())
				return nil
			}
			dropLease(key, f) // this process's unit was evicted from under it
		}
		f, err := openLease(key)
		if err != nil {
			return err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
			f.Close()
			return err
		}
		if !sameFile(f, filepath.Join(key, UnitLeaseName)) {
			f.Close() // evicted while we waited: lease its replacement
			continue
		}
		leases.Lock()
		if cur := leases.held[key]; cur != nil {
			f.Close()
		} else {
			leases.held[key] = f
		}
		leases.Unlock()
		touchNow(filepath.Join(key, UnitLeaseName))
		return nil
	}
	return fmt.Errorf("cache unit %s: evicted repeatedly while leasing", dir)
}

// EvictUnit removes dir unless any process — this one included — holds its lease or is
// installing into it, reporting whether it did. The unit is renamed aside before it is
// deleted, so no run sees it half removed.
func EvictUnit(dir string) (bool, error) {
	key := lockKey(dir)
	leases.Lock()
	ours := leases.held[key] != nil
	leases.Unlock()
	if ours {
		return false, nil
	}
	lease, err := openLease(key)
	if err != nil {
		return false, err
	}
	defer lease.Close()
	if syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false, nil
	}
	if install, err := os.Open(filepath.Join(key, installLockName)); err == nil {
		defer install.Close()
		if syscall.Flock(int(install.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			return false, nil
		}
	}
	tomb := filepath.Join(filepath.Dir(key), "."+filepath.Base(key)+tombstoneMark+strconv.Itoa(os.Getpid())+"-"+strconv.FormatInt(time.Now().UnixNano(), 36))
	if err := os.Rename(key, tomb); err != nil {
		return false, err
	}
	return true, RemoveAllWritable(tomb)
}

// ResetUnit empties dir — evicts it and leases a fresh one — unless another process
// holds it, reporting whether it did. This process holds dir leased afterwards either way.
func ResetUnit(dir string) (bool, error) {
	key := lockKey(dir)
	leases.Lock()
	f := leases.held[key]
	delete(leases.held, key)
	leases.Unlock()
	if f != nil {
		f.Close()
	}
	done, err := EvictUnit(key)
	if lerr := LeaseUnit(key); err == nil {
		err = lerr
	}
	return done, err
}

// WithUnitExclusive runs fn holding dir exclusively, unless another process holds it,
// reporting whether fn ran. This process holds dir leased afterwards either way.
func WithUnitExclusive(dir string, fn func() error) (bool, error) {
	key := lockKey(dir)
	leases.Lock()
	f := leases.held[key]
	delete(leases.held, key)
	leases.Unlock()
	if f != nil {
		f.Close()
	}
	defer func() { _ = LeaseUnit(key) }()
	lease, err := openLease(key)
	if err != nil {
		return false, err
	}
	defer lease.Close()
	if syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false, nil
	}
	return true, fn()
}

// UnitLastUse is when dir was last leased; ok is false for a directory never leased.
func UnitLastUse(dir string) (time.Time, bool) {
	fi, err := os.Stat(filepath.Join(dir, UnitLeaseName))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// IsTombstone reports whether name is a unit renamed aside by EvictUnit, left behind by
// an eviction that didn't finish deleting it.
func IsTombstone(name string) bool {
	return strings.HasPrefix(name, ".") && strings.Contains(name, tombstoneMark)
}

// ClearUnit removes everything in dir but its lease and install lock, so a failed
// install leaves the unit empty without stranding the runs waiting on its locks.
func ClearUnit(dir string) error {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ents {
		if e.Name() == UnitLeaseName || e.Name() == installLockName {
			continue
		}
		if err := RemoveAllWritable(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// RemoveAllWritable is os.RemoveAll that first makes directories writable: Go's module
// cache is read-only.
func RemoveAllWritable(path string) error {
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// AcquireInstallLock leases dir and takes its install lock exclusively, waiting up to
// timeout for another installer. Release it with ReleaseInstallLock.
func AcquireInstallLock(dir string, timeout time.Duration) (*os.File, error) {
	if err := LeaseUnit(dir); err != nil {
		return nil, fmt.Errorf("leasing %s: %w", dir, err)
	}
	lockPath := filepath.Join(dir, installLockName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", lockPath, err)
	}
	deadline := time.Now().Add(timeout)
	for syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timeout acquiring install lock at %s after %v", lockPath, timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return f, nil
}

// ReleaseInstallLock releases an install lock; the lease stays held.
func ReleaseInstallLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

// openLease opens dir's lease file, creating it group-writable so any run sharing the
// cache's group can record its use.
func openLease(dir string) (*os.File, error) {
	path := filepath.Join(dir, UnitLeaseName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o664)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err == nil && fi.Mode().Perm() != 0o664 {
		_ = f.Chmod(0o664) // only the creator can; umask would otherwise drop group write
	}
	return f, nil
}

func dropLease(key string, f *os.File) {
	leases.Lock()
	if leases.held[key] == f {
		delete(leases.held, key)
	}
	leases.Unlock()
	f.Close()
}

// sameFile reports whether f is still the file at path — false once its unit was renamed
// aside.
func sameFile(f *os.File, path string) bool {
	a, err := f.Stat()
	if err != nil {
		return false
	}
	b, err := os.Stat(path)
	return err == nil && os.SameFile(a, b)
}

// touchNow sets path's times to now, which needs only write permission (not ownership).
func touchNow(path string) {
	_ = unix.UtimesNanoAt(unix.AT_FDCWD, path, nil, 0)
}
