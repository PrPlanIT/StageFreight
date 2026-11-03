package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// otherProcessHolds starts another process holding a shared flock on root's lock file.
func otherProcessHolds(t *testing.T, root string) func() {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	cmd := exec.Command("flock", "-s", filepath.Join(root, CacheLockName), "sleep", "30")
	// flock runs the command as a child that inherits the locked fd: end the whole
	// process group, or the child keeps the lock after flock itself is gone.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait until the shared lock is actually held: an exclusive probe stops succeeding.
	for i := 0; i < 200; i++ {
		probe := exec.Command("flock", "-x", "-n", filepath.Join(root, CacheLockName), "true")
		if probe.Run() != nil {
			break
		}
	}
	return func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }
}

// No other run holds the root: eviction may take it.
func TestExclusiveCacheRoot_FreeRoot(t *testing.T) {
	root := t.TempDir()
	release, ok := ExclusiveCacheRoot(root)
	if !ok {
		t.Fatal("an unused cache root must be taken exclusively")
	}
	release()
	if _, err := os.Stat(filepath.Join(root, CacheLockName)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}
}

// Another run holds the root: eviction must not take it (the GC defers).
func TestExclusiveCacheRoot_OtherRunHoldsIt(t *testing.T) {
	root := t.TempDir()
	stop := otherProcessHolds(t, root)
	defer stop()
	if _, ok := ExclusiveCacheRoot(root); ok {
		t.Fatal("eviction took a cache root another run is using")
	}
	stop()
	// The kill is asynchronous: the lock is released once the holder has exited.
	deadline := time.Now().Add(5 * time.Second)
	for {
		release, ok := ExclusiveCacheRoot(root)
		if ok {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("once the other run ends, eviction may take the root")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A process's own shared hold upgrades for eviction and is restored afterwards.
func TestExclusiveCacheRoot_OwnHoldUpgradesAndRestores(t *testing.T) {
	root := t.TempDir()
	HoldCacheRoot(root)
	release, ok := ExclusiveCacheRoot(root)
	if !ok {
		t.Fatal("the process's own shared hold must not block its own eviction")
	}
	release()
	// Restored: a separate process can no longer take the root exclusively.
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skip("flock(1) not available")
	}
	probe := exec.Command("flock", "-x", "-n", filepath.Join(root, CacheLockName), "true")
	if probe.Run() == nil {
		t.Fatal("this process's shared hold was not restored after eviction")
	}
}

// A lock file another user created, readable but not writable here, still locks: flock
// needs only a read-only descriptor.
func TestExclusiveCacheRoot_ReadOnlyLockFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, CacheLockName), nil, 0o444); err != nil {
		t.Fatal(err)
	}
	release, ok := ExclusiveCacheRoot(root)
	if !ok {
		t.Fatal("a read-only lock file must still be lockable")
	}
	release()
}

// Two spellings of one root share one lock: this process's own hold never blocks its
// own eviction.
func TestExclusiveCacheRoot_SpellingsShareOneLock(t *testing.T) {
	root := t.TempDir()
	HoldCacheRoot(root + "/")
	release, ok := ExclusiveCacheRoot(filepath.Join(root, ".", ""))
	if !ok {
		t.Fatal("another spelling of a held root must upgrade this process's own lock")
	}
	release()
}
