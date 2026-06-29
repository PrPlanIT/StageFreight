package commit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	git "github.com/go-git/go-git/v5"
)

// Adversarial lifecycle matrix for the commit-path worktree preservation guard.
//
// These are deliberately scenario tests, not unit nits: each one drives the guard
// through a real go-git repo + index and asserts the transactional invariant for a
// specific failure mode (pre-commit wipe, legitimate hook rewrite, delete intent,
// rename, mode-only/CRLF over-capture, restore failure, orphan discovery, and
// concurrent/repeated restore).
//
// Signal caveat: real SIGINT/SIGTERM delivery is intentionally NOT exercised here —
// the handler re-raises the signal, which would terminate the test binary. The
// handler's effect is `restore()` (covered directly by the wipe tests), and its
// concurrency contract is covered by the repeated/concurrent restore tests, which
// model "a signal arriving during the deferred restore". End-to-end signal delivery
// belongs in a subprocess/integration test (follow-up).

// ── helpers ──────────────────────────────────────────────────────────────────

func newGuardTestRepo(t *testing.T) (*git.Repository, *git.Worktree, string) {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	return repo, wt, dir
}

// guardSeedCommit writes, stages, and commits a file so the index has a baseline
// blob for it (the content a pre-commit reset reverts unstaged edits to).
func guardSeedCommit(t *testing.T, wt *git.Worktree, dir, name, content string) {
	t.Helper()
	writeTestFile(t, dir, name, content)
	if _, err := wt.Add(name); err != nil {
		t.Fatalf("Add %s: %v", name, err)
	}
	if _, err := wt.Commit("seed "+name, commitOpts("guard")); err != nil {
		t.Fatalf("Commit %s: %v", name, err)
	}
}

func guardArtifactNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(snapshotArtifactDir(dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "snapshot-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func guardReadFile(t *testing.T, path string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

type guardLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *guardLogger) fn() func(stream, line string) {
	return func(stream, line string) {
		l.mu.Lock()
		l.lines = append(l.lines, line)
		l.mu.Unlock()
	}
}

func (l *guardLogger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ln := range l.lines {
		if strings.Contains(ln, sub) {
			return true
		}
	}
	return false
}

// ── matrix ───────────────────────────────────────────────────────────────────

// A clean worktree is inert: no snapshot, no artifact, Close is a no-op.
func TestGuard_CleanWorktreeIsInert(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "x")

	g, err := captureWorktreeGuard(repo, wt, dir, (&guardLogger{}).fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if names := guardArtifactNames(t, dir); len(names) != 0 {
		t.Errorf("clean worktree must not persist an artifact, got %v", names)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// The core fix: an unstaged edit reset to the index baseline (the pre-commit
// stash signature) is restored to the operator's content; artifact cleared.
func TestGuard_BaselineWipeRestores(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "committed")
	writeTestFile(t, dir, "f.txt", "operator wip") // unstaged edit

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if n := len(guardArtifactNames(t, dir)); n != 1 {
		t.Fatalf("expected 1 artifact after capture, got %d", n)
	}

	// pre-commit wipe: worktree reset to the index/HEAD baseline.
	writeTestFile(t, dir, "f.txt", "committed")

	if err := g.Close(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got, _ := guardReadFile(t, filepath.Join(dir, "f.txt")); got != "operator wip" {
		t.Errorf("baseline wipe not restored: got %q want %q", got, "operator wip")
	}
	if n := len(guardArtifactNames(t, dir)); n != 0 {
		t.Errorf("artifact not cleared after clean restore, got %d", n)
	}
}

// A hook that rewrites the file to genuinely NEW content is not a wipe: restore
// must refuse to overwrite, retain the artifact, and surface the conflict loudly —
// without failing the commit.
func TestGuard_DivergenceIsConflictNotClobbered(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "orig")
	writeTestFile(t, dir, "f.txt", "operator edit") // unstaged

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// A formatter/codegen hook writes brand-new content (≠ snapshot, ≠ baseline).
	writeTestFile(t, dir, "f.txt", "FORMATTED BY HOOK")

	if err := g.Close(); err != nil {
		t.Fatalf("a conflict must not be a hard error: %v", err)
	}
	if got, _ := guardReadFile(t, filepath.Join(dir, "f.txt")); got != "FORMATTED BY HOOK" {
		t.Errorf("conflict must NOT clobber on-disk content: got %q", got)
	}
	if n := len(guardArtifactNames(t, dir)); n != 1 {
		t.Errorf("conflict must RETAIN the artifact, got %d", n)
	}
	// The warning block is part of the contract: commit-succeeded statement,
	// conflict headline, the exact artifact path, and a reconciliation instruction.
	for _, want := range []string{
		"WORKTREE PRESERVATION CONFLICT",
		"commit SUCCEEDED",
		"MANUAL RECONCILIATION REQUIRED",
		g.artifactPath,
	} {
		if !lc.contains(want) {
			t.Errorf("conflict warning block missing %q; logs:\n%s", want, strings.Join(lc.lines, "\n"))
		}
	}
}

// The trust-defining real-world case: a file is partially staged, the operator has
// a further unstaged hunk, and a formatter hook rewrites the WHOLE file. Prove:
//   - the formatter's on-disk output survives (not clobbered by the snapshot),
//   - the operator's pre-hook unstaged content survives verbatim in the artifact,
//   - the conflict is surfaced unmistakably,
//   - nothing is silently overwritten.
func TestGuard_FormatterRewritesPartiallyStagedFile_ConflictPreservesBoth(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "code.go", "package x\n// v1\n")

	// Stage v2, then add a further unstaged hunk (v3). Index = v2, worktree = v3.
	writeTestFile(t, dir, "code.go", "package x\n// v2 staged\n")
	if _, err := wt.Add("code.go"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	operatorContent := "package x\n// v2 staged\n// v3 operator unstaged hunk\n"
	writeTestFile(t, dir, "code.go", operatorContent)

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// Formatter hook rewrites the entire file to gofmt-style output — new content,
	// neither the operator's v3 nor the index baseline v2.
	formatterContent := "package x\n\n// v2 staged\n// v3 operator unstaged hunk\n"
	writeTestFile(t, dir, "code.go", formatterContent)

	if err := g.Close(); err != nil {
		t.Fatalf("conflict must not be a hard error: %v", err)
	}

	// 1. Formatter output survives on disk.
	if got, _ := guardReadFile(t, filepath.Join(dir, "code.go")); got != formatterContent {
		t.Errorf("formatter output was clobbered: got %q want %q", got, formatterContent)
	}
	// 2. Operator's pre-hook content survives verbatim in the retained artifact.
	names := guardArtifactNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("expected 1 retained artifact, got %d", len(names))
	}
	var art snapshotArtifact
	raw, rerr := os.ReadFile(filepath.Join(snapshotArtifactDir(dir), names[0]))
	if rerr != nil {
		t.Fatalf("read artifact: %v", rerr)
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("unmarshal artifact: %v", err)
	}
	var found bool
	for _, f := range art.Files {
		if f.Path == "code.go" {
			found = true
			if string(f.Content) != operatorContent {
				t.Errorf("artifact lost operator content: got %q want %q", f.Content, operatorContent)
			}
		}
	}
	if !found {
		t.Errorf("artifact does not preserve code.go; files: %+v", art.Files)
	}
	// 3. Conflict surfaced unmistakably.
	if !lc.contains("WORKTREE PRESERVATION CONFLICT") || !lc.contains("code.go") {
		t.Errorf("formatter conflict not surfaced; logs:\n%s", strings.Join(lc.lines, "\n"))
	}
}

// A partially-staged file (index = v2, worktree = v3) keeps its worktree edit
// across a wipe to the staged version; the committed index version is unaffected.
func TestGuard_PartiallyStagedSurvivesWipe(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "v1")

	writeTestFile(t, dir, "f.txt", "v2")
	if _, err := wt.Add("f.txt"); err != nil { // stage v2
		t.Fatalf("Add: %v", err)
	}
	writeTestFile(t, dir, "f.txt", "v3") // further unstaged edit; index stays v2

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// hook wipe resets the worktree to the staged (index) version.
	writeTestFile(t, dir, "f.txt", "v2")

	if err := g.Close(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got, _ := guardReadFile(t, filepath.Join(dir, "f.txt")); got != "v3" {
		t.Errorf("partially-staged worktree edit not preserved: got %q want v3", got)
	}
}

// An operator's unstaged deletion is honored even when a wipe resurrects the file
// at the index baseline.
func TestGuard_DeleteIntentPreserved(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "del.txt", "x")
	if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
		t.Fatalf("rm: %v", err)
	}

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	writeTestFile(t, dir, "del.txt", "x") // wipe resurrects at baseline

	if err := g.Close(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, ok := guardReadFile(t, filepath.Join(dir, "del.txt")); ok {
		t.Errorf("operator deletion not honored: del.txt still present")
	}
}

// A deletion resurrected with DIVERGENT content is a conflict — the file is kept,
// not deleted, and the artifact is retained.
func TestGuard_DeleteResurrectDivergenceIsConflict(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "del.txt", "x")
	if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
		t.Fatalf("rm: %v", err)
	}

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeTestFile(t, dir, "del.txt", "NEW CONTENT") // diverges from baseline

	if err := g.Close(); err != nil {
		t.Fatalf("conflict must not be a hard error: %v", err)
	}
	if got, ok := guardReadFile(t, filepath.Join(dir, "del.txt")); !ok || got != "NEW CONTENT" {
		t.Errorf("divergent resurrection must be kept, not deleted: got %q ok=%v", got, ok)
	}
	if n := len(guardArtifactNames(t, dir)); n != 1 {
		t.Errorf("conflict must retain the artifact, got %d", n)
	}
}

// Rename (go-git status: old deleted + new untracked): the delete side is honored
// on a wipe; the untracked new side is never managed and survives untouched.
func TestGuard_RenameDeletePreservedNewLeftAlone(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "old.txt", "data")
	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	writeTestFile(t, dir, "old.txt", "data") // wipe resurrects old at baseline

	if err := g.Close(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, ok := guardReadFile(t, filepath.Join(dir, "old.txt")); ok {
		t.Errorf("rename delete-side not honored: old.txt resurrected")
	}
	if got, ok := guardReadFile(t, filepath.Join(dir, "new.txt")); !ok || got != "data" {
		t.Errorf("rename new-side (untracked) must be untouched: got %q ok=%v", got, ok)
	}
}

// Over-capture is safe: a file the hook never wipes (e.g. only CRLF/.gitattributes
// dirty) restores as a verified no-op and the artifact is cleared.
func TestGuard_OverCaptureNoOpWhenUntouched(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "content")
	writeTestFile(t, dir, "f.txt", "operator content")

	lc := &guardLogger{}
	g, err := captureWorktreeGuard(repo, wt, dir, lc.fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	// hook does NOT touch f.txt.
	if err := g.Close(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got, _ := guardReadFile(t, filepath.Join(dir, "f.txt")); got != "operator content" {
		t.Errorf("no-op restore altered content: %q", got)
	}
	if n := len(guardArtifactNames(t, dir)); n != 0 {
		t.Errorf("artifact not cleared after no-op restore, got %d", n)
	}
}

// Mode-only dirtiness must never error and must never corrupt content, whether or
// not go-git reports it as dirty.
func TestGuard_ModeOnlyChangeCapturedAndClean(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "s.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "s.sh"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	g, err := captureWorktreeGuard(repo, wt, dir, (&guardLogger{}).fn())
	if err != nil {
		t.Fatalf("capture must not error on mode-only dirtiness: %v", err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got, _ := guardReadFile(t, filepath.Join(dir, "s.sh")); got != "#!/bin/sh\n" {
		t.Errorf("mode-only restore corrupted content: %q", got)
	}
}

// Repeated restore (models repeated signal delivery) is idempotent and never
// double-applies or panics.
func TestGuard_RestoreIdempotentAndFinalizeOnce(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "base")
	writeTestFile(t, dir, "f.txt", "wip")

	g, err := captureWorktreeGuard(repo, wt, dir, (&guardLogger{}).fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeTestFile(t, dir, "f.txt", "base") // wipe

	for i := 0; i < 4; i++ {
		if err := g.Close(); err != nil {
			t.Fatalf("restore #%d: %v", i, err)
		}
	}
	if got, _ := guardReadFile(t, filepath.Join(dir, "f.txt")); got != "wip" {
		t.Errorf("idempotent restore changed result: %q", got)
	}
}

// Concurrent restore (models the signal handler racing the deferred Close())
// serializes via the mutex, finalizes once, and never panics or double-clears.
func TestGuard_ConcurrentRestoreOrdering(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "base")
	writeTestFile(t, dir, "f.txt", "wip")

	g, err := captureWorktreeGuard(repo, wt, dir, (&guardLogger{}).fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	writeTestFile(t, dir, "f.txt", "base") // wipe

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.Close() }()
	}
	wg.Wait()

	if got, _ := guardReadFile(t, filepath.Join(dir, "f.txt")); got != "wip" {
		t.Errorf("concurrent restore produced wrong result: %q", got)
	}
	if n := len(guardArtifactNames(t, dir)); n != 0 {
		t.Errorf("artifact should be cleared exactly once, got %d", n)
	}
}

// A failed restore (models an interrupted/blocked restore) propagates an error and
// RETAINS the recovery artifact for manual recovery.
func TestGuard_RestoreFailureRetainsArtifact(t *testing.T) {
	repo, wt, dir := newGuardTestRepo(t)
	guardSeedCommit(t, wt, dir, "f.txt", "base")
	writeTestFile(t, dir, "f.txt", "wip")

	g, err := captureWorktreeGuard(repo, wt, dir, (&guardLogger{}).fn())
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// Force a deterministic write failure: replace the file path with a directory.
	p := filepath.Join(dir, "f.txt")
	if err := os.Remove(p); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err = g.Close()
	if err == nil {
		t.Fatalf("expected a restore-failure error")
	}
	if !strings.Contains(err.Error(), "RETAINED") {
		t.Errorf("error must state the artifact was retained: %v", err)
	}
	if n := len(guardArtifactNames(t, dir)); n != 1 {
		t.Errorf("artifact must be retained on restore failure, got %d", n)
	}
}

// An orphaned artifact from a prior hard-killed run is surfaced (named, with its
// paths) on the next run — and NOT auto-applied or deleted.
func TestGuard_SurfaceOrphanedSnapshot(t *testing.T) {
	dir := t.TempDir()
	adir := snapshotArtifactDir(dir)
	if err := os.MkdirAll(adir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	art := snapshotArtifact{
		CreatedAt: "2026-01-01T00:00:00Z",
		RepoRoot:  dir,
		Files:     []snapshotFileJSON{{Path: "wip.txt", Content: []byte("stranded")}},
	}
	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	apath := filepath.Join(adir, "snapshot-123.json")
	if err := os.WriteFile(apath, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	lc := &guardLogger{}
	surfaceOrphanedSnapshots(dir, lc.fn())

	if !lc.contains("orphaned worktree recovery snapshot") {
		t.Errorf("orphan not surfaced; logs: %v", lc.lines)
	}
	if !lc.contains("wip.txt") {
		t.Errorf("orphan log must name the stranded path; logs: %v", lc.lines)
	}
	if _, err := os.Stat(apath); err != nil {
		t.Errorf("surfacing must not delete the artifact: %v", err)
	}
}
