package paths

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// otherRun holds dir's lease shared from a separate open file, as another process would.
func otherRun(t *testing.T, dir string) func() {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, UnitLeaseName))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	return func() { f.Close() }
}

func forgetLeases(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		leases.Lock()
		for k, f := range leases.held {
			f.Close()
			delete(leases.held, k)
		}
		leases.Unlock()
	})
}

// A leased unit records its use and can't be evicted — by this process or another —
// until no run holds it.
func TestLeaseUnit_BlocksEviction(t *testing.T) {
	forgetLeases(t)
	dir := filepath.Join(t.TempDir(), "unit")
	if err := LeaseUnit(dir); err != nil {
		t.Fatal(err)
	}
	if last, ok := UnitLastUse(dir); !ok || time.Since(last) > time.Minute {
		t.Fatalf("last use = %v %v, want now", last, ok)
	}
	if done, err := EvictUnit(dir); done || err != nil {
		t.Fatalf("evicting this process's own lease: done=%v err=%v", done, err)
	}

	release := otherRun(t, dir)
	leases.Lock()
	for k, f := range leases.held {
		f.Close()
		delete(leases.held, k)
	}
	leases.Unlock()
	if done, _ := EvictUnit(dir); done {
		t.Fatal("a unit another run holds must not be evicted")
	}
	release()
	if done, err := EvictUnit(dir); !done || err != nil {
		t.Fatalf("an unheld unit must be evicted: done=%v err=%v", done, err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the evicted unit must be gone")
	}
	if ents, _ := os.ReadDir(filepath.Dir(dir)); len(ents) != 0 {
		t.Fatalf("eviction left %v behind", ents)
	}
}

// A unit being installed into isn't evicted, even by an installer that doesn't lease.
func TestEvictUnit_SkipsInstallInProgress(t *testing.T) {
	forgetLeases(t)
	dir := filepath.Join(t.TempDir(), "unit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, installLockName), os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if done, _ := EvictUnit(dir); done {
		t.Fatal("a unit under install must not be evicted")
	}
}

// A lease held on a unit that was since evicted is replaced by one on the new unit.
func TestLeaseUnit_ReleasesAfterEviction(t *testing.T) {
	forgetLeases(t)
	dir := filepath.Join(t.TempDir(), "unit")
	if err := LeaseUnit(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+".gone"); err != nil {
		t.Fatal(err)
	}
	if err := LeaseUnit(dir); err != nil {
		t.Fatal(err)
	}
	release := otherRun(t, dir)
	defer release()
	leases.Lock()
	f := leases.held[lockKey(dir)]
	leases.Unlock()
	if f == nil || !sameFile(f, filepath.Join(dir, UnitLeaseName)) {
		t.Fatal("the lease must be on the current unit")
	}
}

// ResetUnit empties a unit no other run holds and leaves it leased; one another run
// holds is kept.
func TestResetUnit(t *testing.T) {
	forgetLeases(t)
	dir := filepath.Join(t.TempDir(), "unit")
	if err := LeaseUnit(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if done, err := ResetUnit(dir); !done || err != nil {
		t.Fatalf("reset: done=%v err=%v", done, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "db")); err == nil {
		t.Fatal("a reset unit must be empty")
	}
	if done, _ := EvictUnit(dir); done {
		t.Fatal("a reset unit must still be leased")
	}

	if err := os.WriteFile(filepath.Join(dir, "db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := otherRun(t, dir)
	defer release()
	if done, _ := ResetUnit(dir); done {
		t.Fatal("a unit another run holds must not be reset")
	}
	if _, err := os.Stat(filepath.Join(dir, "db")); err != nil {
		t.Fatal("a kept unit must keep its contents")
	}
}

// WithUnitExclusive runs only while no other run holds the unit.
func TestWithUnitExclusive(t *testing.T) {
	forgetLeases(t)
	dir := filepath.Join(t.TempDir(), "unit")
	if err := LeaseUnit(dir); err != nil {
		t.Fatal(err)
	}
	ran, err := WithUnitExclusive(dir, func() error { return nil })
	if !ran || err != nil {
		t.Fatalf("unheld: ran=%v err=%v", ran, err)
	}
	release := otherRun(t, dir)
	defer release()
	if ran, _ := WithUnitExclusive(dir, func() error { t.Fatal("must not run"); return nil }); ran {
		t.Fatal("must not run while another run holds the unit")
	}
}

// The install lock leases the unit, so it can't be evicted while being installed.
func TestAcquireInstallLock_Leases(t *testing.T) {
	forgetLeases(t)
	dir := filepath.Join(t.TempDir(), "unit")
	f, err := AcquireInstallLock(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ReleaseInstallLock(f)
	if _, ok := UnitLastUse(dir); !ok {
		t.Fatal("installing must lease the unit")
	}
	if _, err := AcquireInstallLock(dir, 300*time.Millisecond); err == nil {
		t.Fatal("a second installer must wait for the first")
	}
}
