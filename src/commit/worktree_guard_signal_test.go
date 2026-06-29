package commit

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
)

// Subprocess signal-integration tests for the worktree guard.
//
// In-process testing cannot validate the signal handler's re-raise contract: the
// handler restores and then re-raises the signal under its default disposition,
// which terminates the process. So these tests re-exec the test binary as a child
// (the standard Go HelperProcess pattern), drive it into the "captured snapshot,
// worktree wiped, hook in flight" window, deliver a real signal, and then — from
// the parent — assert the on-disk and exit outcomes.
//
// The child is TestGuardSignalHelperProcess, a no-op unless GUARD_HELPER is set.

const (
	guardHelperEnv   = "GUARD_HELPER"   // mode: "term" | "block"
	guardHelperDir   = "GUARD_DIR"      // repo dir (already created by parent)
	guardHelperReady = "GUARD_READY"    // path the child touches when armed
	guardOperatorWIP = "operator wip\n" // preserved unstaged content
	guardBaseline    = "base\n"         // committed/index baseline
)

// TestGuardSignalHelperProcess is the re-exec child. It builds a real repo with an
// unstaged edit, captures the guard (arming the signal handler), simulates a
// pre-commit wipe back to the baseline, signals readiness, and blocks — emulating
// the exact window in which the original work was stranded. On SIGTERM/SIGINT the
// guard's handler restores and re-raises, killing this process by the signal.
func TestGuardSignalHelperProcess(t *testing.T) {
	mode := os.Getenv(guardHelperEnv)
	if mode == "" {
		return // normal test run — do nothing
	}
	dir := os.Getenv(guardHelperDir)
	ready := os.Getenv(guardHelperReady)

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("child PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("child Worktree: %v", err)
	}
	writeTestFile(t, dir, "f.txt", guardBaseline)
	if _, err := wt.Add("f.txt"); err != nil {
		t.Fatalf("child Add: %v", err)
	}
	if _, err := wt.Commit("seed", commitOpts("guard")); err != nil {
		t.Fatalf("child Commit: %v", err)
	}

	// Operator's unstaged edit, then capture (arms the handler), then wipe to
	// baseline (what pre-commit does before running hooks).
	writeTestFile(t, dir, "f.txt", guardOperatorWIP)
	g, err := captureWorktreeGuard(repo, wt, dir, func(_, _ string) {})
	if err != nil {
		t.Fatalf("child capture: %v", err)
	}
	_ = g
	writeTestFile(t, dir, "f.txt", guardBaseline)

	// Arm complete. Tell the parent, then block until the signal lands.
	if err := os.WriteFile(ready, []byte("1"), 0o600); err != nil {
		t.Fatalf("child ready signal: %v", err)
	}
	select {} // the guard's signal handler terminates us
}

// reExecGuardChild starts the helper child in the given mode against repoDir and
// returns the running command plus the readiness path.
func reExecGuardChild(t *testing.T, mode, repoDir string) (*exec.Cmd, string) {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=TestGuardSignalHelperProcess", "-test.timeout=60s")
	cmd.Env = append(os.Environ(),
		guardHelperEnv+"="+mode,
		guardHelperDir+"="+repoDir,
		guardHelperReady+"="+ready,
	)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting child: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return cmd, ready
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func skipIfNoSignals(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal semantics not applicable on windows")
	}
}

// SIGTERM during the hook window: the handler restores the operator's content and
// the process exits via SIGTERM (default disposition after re-raise). The clean
// restore also clears the recovery artifact.
func TestGuardSignal_SIGTERMRestoresAndReRaises(t *testing.T) {
	skipIfNoSignals(t)
	dir := t.TempDir()
	cmd, ready := reExecGuardChild(t, "term", dir)
	waitForFile(t, ready, 30*time.Second)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	err := cmd.Wait()

	// Exit must reflect death-by-SIGTERM, not a normal/zero exit.
	assertSignaled(t, err, syscall.SIGTERM)

	// Operator content restored on disk.
	if got, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(got) != guardOperatorWIP {
		t.Errorf("SIGTERM did not restore operator content: got %q want %q", got, guardOperatorWIP)
	}
	// Clean restore clears the artifact.
	if n := len(guardArtifactNames(t, dir)); n != 0 {
		t.Errorf("artifact not cleared after signal restore, got %d", n)
	}
}

// SIGINT behaves identically to SIGTERM (both registered, both re-raised).
func TestGuardSignal_SIGINTRestores(t *testing.T) {
	skipIfNoSignals(t)
	dir := t.TempDir()
	cmd, ready := reExecGuardChild(t, "term", dir)
	waitForFile(t, ready, 30*time.Second)

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal: %v", err)
	}
	err := cmd.Wait()
	assertSignaled(t, err, syscall.SIGINT)

	if got, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(got) != guardOperatorWIP {
		t.Errorf("SIGINT did not restore operator content: got %q", got)
	}
}

// Repeated signal spam must not double-restore or corrupt: the result is still the
// single operator content and the process dies by signal. finalize-once + the
// idempotent restore guarantee this.
func TestGuardSignal_RepeatedSignalsNoCorruption(t *testing.T) {
	skipIfNoSignals(t)
	dir := t.TempDir()
	cmd, ready := reExecGuardChild(t, "term", dir)
	waitForFile(t, ready, 30*time.Second)

	for i := 0; i < 5; i++ {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(2 * time.Millisecond)
	}
	err := cmd.Wait()
	assertSignaled(t, err, syscall.SIGTERM)

	if got, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(got) != guardOperatorWIP {
		t.Errorf("repeated signals corrupted restore: got %q want %q", got, guardOperatorWIP)
	}
}

// Hard kill (SIGKILL, uncatchable): no restore runs, the worktree stays wiped, and
// the persisted artifact is ORPHANED. A subsequent run must surface it (the
// discoverability invariant for the unavoidable edge).
func TestGuardSignal_SIGKILLLeavesDiscoverableOrphan(t *testing.T) {
	skipIfNoSignals(t)
	dir := t.TempDir()
	cmd, ready := reExecGuardChild(t, "block", dir)
	waitForFile(t, ready, 30*time.Second)

	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()

	// No restore ran → worktree still at the wiped baseline.
	if got, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(got) != guardBaseline {
		t.Errorf("SIGKILL path should not have restored; got %q", got)
	}
	// The artifact is orphaned (persisted pre-hook, restore never ran).
	if n := len(guardArtifactNames(t, dir)); n != 1 {
		t.Fatalf("expected 1 orphaned artifact after hard kill, got %d", n)
	}
	// A subsequent run surfaces it — and does NOT auto-apply or delete it.
	lc := &guardLogger{}
	surfaceOrphanedSnapshots(dir, lc.fn())
	if !lc.contains("orphaned worktree recovery snapshot") || !lc.contains("f.txt") {
		t.Errorf("orphan not surfaced on subsequent run; logs:\n%s", strings.Join(lc.lines, "\n"))
	}
	if n := len(guardArtifactNames(t, dir)); n != 1 {
		t.Errorf("surfacing must not delete the orphan, got %d", n)
	}
}

func assertSignaled(t *testing.T, waitErr error, want syscall.Signal) {
	t.Helper()
	exitErr, ok := waitErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected the child to die by signal %v, got wait err: %v", want, waitErr)
	}
	ws, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("no WaitStatus available: %v", exitErr)
	}
	if !ws.Signaled() {
		t.Fatalf("child did not die by a signal (exit code %d)", ws.ExitStatus())
	}
	if ws.Signal() != want {
		t.Fatalf("child died by %v, want %v", ws.Signal(), want)
	}
}
