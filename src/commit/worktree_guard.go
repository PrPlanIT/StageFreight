package commit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// worktreeGuard provides transactional preservation of the operator's unstaged
// worktree state across pre-commit hook execution.
//
// Invariant (StageFreight transactional integrity):
//
//	No commit operation may DESTROY, DISCARD, or ORPHAN unstaged tracked worktree
//	changes that are not part of the commit — across hook-failure paths and
//	recoverable process interruption (SIGINT/SIGTERM).
//
// Note the invariant is about LOSS, not "snapshot always wins". A hook that
// legitimately rewrites a file (formatter, codegen) is not a loss; blindly
// overwriting it with the snapshot would itself be the destructive act. So
// restore is conflict-classified, not unconditional (see restore).
//
// Why this exists: the repo's pre-commit framework stashes unstaged changes to
// its own cache, resets the worktree so hooks see only staged content, then
// restores. If that run is interrupted before its restore, the operator's edits
// are stranded. StageFreight orchestrates the commit, so it owns the invariant —
// the Replay path already models worktree preservation as first-class
// (captureWorktree/restoreWorktree in gogit_replay.go); this brings the commit
// path to the same bar.
//
// Coverage matrix:
//
//	normal error / hook-failure exit ....... defer Close() → restore
//	graceful interrupt (SIGINT/SIGTERM) .... signal handler → restore, then
//	                                         RE-RAISE under the default disposition
//	                                         (never os.Exit from a side goroutine)
//	hard kill / power loss after snapshot .. persisted artifact, surfaced (not
//	                                         auto-applied) on the next run. The
//	                                         invariant in this edge is artifact
//	                                         DISCOVERABILITY, not auto-restoration.
type worktreeGuard struct {
	repoRoot     string
	snapshot     []guardedFile
	artifactPath string
	log          func(stream, line string)

	mu           sync.Mutex
	finalized    bool
	sigCh        chan os.Signal
	done         chan struct{}
	watchStopped bool
}

const guardLogStream = "worktree_guard"

// guardedFile is one preserved path plus the metadata restore needs to classify
// a later divergence safely.
type guardedFile struct {
	path    string
	content []byte      // worktree content at capture (nil when deleted)
	mode    os.FileMode // worktree perm bits at capture
	deleted bool        // operator had deleted this tracked file in the worktree

	// baseline is the index blob hash for this path at capture time — the content
	// a pre-commit stash/reset reverts the worktree to. It is the signature of a
	// "clean wipe": if a path later equals this baseline, the operator's edit was
	// reverted (safe to re-apply). Zero when the path had no index entry.
	baseline plumbing.Hash
}

func snapshotArtifactDir(repoRoot string) string {
	return filepath.Join(repoRoot, ".git", "stagefreight", "worktree-snapshots")
}

// snapshotFileJSON is the on-disk serialization of one preserved path. Content is
// base64-encoded by encoding/json ([]byte), so binary files round-trip safely.
type snapshotFileJSON struct {
	Path     string `json:"path"`
	Mode     uint32 `json:"mode"`
	Deleted  bool   `json:"deleted"`
	Baseline string `json:"baseline,omitempty"`
	Content  []byte `json:"content,omitempty"`
}

type snapshotArtifact struct {
	CreatedAt string             `json:"created_at"`
	RepoRoot  string             `json:"repo_root"`
	Files     []snapshotFileJSON `json:"files"`
}

// captureWorktreeGuard snapshots the operator's at-risk unstaged worktree state
// BEFORE any hook runs, persists a recovery artifact, and arms a signal handler.
//
// The at-risk set is every path whose worktree content diverges from the index
// (Worktree != Unmodified) and is tracked (not Untracked) — precisely what a
// pre-commit stash resets. Fully-staged-and-clean paths (Worktree == Unmodified)
// are the commit target and are inherently excluded; partially-staged paths keep
// their worktree content preserved without affecting the committed index version.
//
// Capture deliberately ERRS TOWARD OVER-PRESERVATION: edge cases that make go-git
// report a path dirty when "logically" unchanged (CRLF/.gitattributes
// normalization, mode-only changes) are captured too. That is safe — restore is
// a verified no-op when the worktree already matches the snapshot — and never the
// reverse of safe, which is the only acceptable bias for a preservation layer.
//
// When nothing is at risk the guard is inert (Close is a no-op) and neither an
// artifact nor a signal handler is created.
func captureWorktreeGuard(repo *git.Repository, wt *git.Worktree, repoRoot string, log func(stream, line string)) (*worktreeGuard, error) {
	g := &worktreeGuard{repoRoot: repoRoot, log: logOrNoop(log), done: make(chan struct{})}

	st, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("worktree guard: reading status: %w", err)
	}

	// Index blob hashes = the per-path baseline a pre-commit reset reverts to.
	baseline := map[string]plumbing.Hash{}
	if idx, ierr := repo.Storer.Index(); ierr == nil {
		for _, e := range idx.Entries {
			baseline[e.Name] = e.Hash
		}
	}

	for path, s := range st {
		// Untracked files survive a stash/reset and are not ours to manage.
		// Worktree == Unmodified means the file matches the index (fully staged or
		// pristine) — nothing for a hook reset to wipe, so nothing to preserve.
		if s.Worktree == git.Unmodified || s.Worktree == git.Untracked {
			continue
		}
		gf := guardedFile{path: path, baseline: baseline[path]}
		full := filepath.Join(repoRoot, path)
		info, serr := os.Stat(full)
		switch {
		case serr != nil && os.IsNotExist(serr):
			gf.deleted = true // worktree-deleted tracked file: preserve the deletion
		case serr != nil:
			return nil, fmt.Errorf("worktree guard: stat %s: %w", path, serr)
		default:
			data, rerr := os.ReadFile(full)
			if rerr != nil {
				return nil, fmt.Errorf("worktree guard: read %s: %w", path, rerr)
			}
			gf.content = data
			gf.mode = info.Mode().Perm()
		}
		g.snapshot = append(g.snapshot, gf)
	}

	// Nothing at risk → inert guard.
	if len(g.snapshot) == 0 {
		g.finalized = true
		return g, nil
	}

	// Persist BEFORE running hooks. Without pre-hook persisted state the whole
	// mechanism is lossy under termination — this is the load-bearing decision.
	// A persistence failure means we would let a hook reset these files with no
	// on-disk backup, so refuse to proceed.
	if err := g.persist(); err != nil {
		return nil, fmt.Errorf("worktree guard: persisting recovery snapshot (refusing to run hooks without a backup): %w", err)
	}

	g.installSignalHandler()
	g.logf("captured worktree snapshot: %d path(s) preserved → %s", len(g.snapshot), g.artifactPath)
	return g, nil
}

func (g *worktreeGuard) persist() error {
	dir := snapshotArtifactDir(g.repoRoot)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	files := make([]snapshotFileJSON, 0, len(g.snapshot))
	for _, f := range g.snapshot {
		bl := ""
		if !f.baseline.IsZero() {
			bl = f.baseline.String()
		}
		files = append(files, snapshotFileJSON{
			Path:     f.path,
			Mode:     uint32(f.mode),
			Deleted:  f.deleted,
			Baseline: bl,
			Content:  f.content,
		})
	}
	payload := snapshotArtifact{
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		RepoRoot:  g.repoRoot,
		Files:     files,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fmt.Sprintf("snapshot-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	g.artifactPath = path
	return nil
}

// installSignalHandler arms a SIGINT/SIGTERM handler. On receipt it restores the
// snapshot (the case Go's defer cannot cover) and then RE-RAISES the signal under
// its default disposition, so the process exits with correct shell semantics.
//
// It deliberately does NOT os.Exit() from this side goroutine: that would bypass
// remaining defers process-wide and could tear the process down mid-mutation in
// the main goroutine. Re-raising hands termination back to the runtime's default
// handler after our cleanup. The restore here writes only the snapshotted
// unstaged paths, which are disjoint from the index/objects a concurrent commit
// touches; combined with finalize-once it cannot double-apply with the main
// goroutine's deferred Close().
func (g *worktreeGuard) installSignalHandler() {
	g.sigCh = make(chan os.Signal, 1)
	signal.Notify(g.sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-g.done:
			return
		case sig := <-g.sigCh:
			g.mu.Lock()
			done := g.finalized
			g.mu.Unlock()
			if !done {
				g.logf("interrupt (%s) received — restoring worktree before exit", sig)
				if err := g.restore(); err != nil {
					g.logf("CRITICAL: worktree restore on interrupt FAILED: %v", err)
				}
			}
			// Re-raise under the default disposition. Never os.Exit() here.
			signal.Stop(g.sigCh)
			signal.Reset(sig)
			if ssig, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(syscall.Getpid(), ssig)
			}
		}
	}()
}

// Close finalizes the guard on any exit path: restore (idempotent + conflict
// classified), tear down the signal handler, clear the artifact on a clean
// outcome. Safe to call multiple times.
func (g *worktreeGuard) Close() error {
	return g.restore()
}

// restore re-applies the snapshot with conflict classification. For each path:
//
//	missing now, had content ......... WIPED to deletion → safe restore
//	equals snapshot .................. already operator content → no-op
//	equals index baseline ............ clean stash/reset wipe → safe restore
//	arbitrary divergence ............. a hook/operator wrote NEW content →
//	                                   CONFLICT: refuse to overwrite, retain the
//	                                   artifact, surface loudly (no silent loss —
//	                                   both the on-disk content and the snapshot
//	                                   survive for manual reconciliation)
//
// A hard write FAILURE while applying a safe-restore is returned as an error and
// retains the artifact. Conflicts are surfaced loudly and retain the artifact but
// do NOT fail the commit (the commit itself succeeded; the operator must
// reconcile). Idempotent and finalize-once.
func (g *worktreeGuard) restore() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finalized {
		return nil
	}

	var failures, conflicts []string
	for _, f := range g.snapshot {
		full := filepath.Join(g.repoRoot, f.path)
		cur, readErr := os.ReadFile(full)
		exists := readErr == nil

		if f.deleted {
			// Operator had deleted this tracked file. Honor that only when the
			// current state is the clean wipe (file resurrected at the index
			// baseline); a divergent resurrection is a conflict we must not delete.
			switch {
			case !exists:
				// still deleted — nothing to do
			case g.matchesBaseline(f, cur):
				if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
					failures = append(failures, fmt.Sprintf("%s: re-delete: %v", f.path, err))
				}
			default:
				conflicts = append(conflicts, f.path)
			}
			continue
		}

		switch {
		case exists && bytes.Equal(cur, f.content):
			// already the operator's content (pre-commit restored it) — no-op
		case !exists:
			// content was wiped to deletion — safe to restore
			if err := g.writeFile(full, f); err != nil {
				failures = append(failures, err.Error())
			}
		case g.matchesBaseline(f, cur):
			// clean reset to the index baseline (classic pre-commit stash) — safe
			if err := g.writeFile(full, f); err != nil {
				failures = append(failures, err.Error())
			}
		default:
			// arbitrary divergence — a hook/formatter/operator wrote NEW content.
			// Refuse to overwrite; preserve both sides.
			conflicts = append(conflicts, f.path)
		}
	}

	g.finalized = true
	g.stopSignalWatch()

	if len(conflicts) > 0 {
		// Operator visibility is part of the transactional contract here: the
		// commit succeeded, but some unstaged worktree state could NOT be safely
		// restored automatically and is now manual reconciliation work. A single
		// log line is too easy to miss in CI/local noise — emit a framed block so
		// the retained artifact is never later read as mysterious residue.
		g.emitConflictWarning(conflicts)
	}
	if len(failures) > 0 {
		// Hard failure: retain artifact, propagate.
		return fmt.Errorf("worktree restore FAILED for %d path(s) — recovery artifact RETAINED at %s: %s",
			len(failures), g.artifactPath, strings.Join(failures, "; "))
	}
	if len(conflicts) > 0 {
		return nil // surfaced above; artifact intentionally retained
	}

	g.removeArtifact()
	g.logf("worktree restored: %d path(s) preserved; recovery artifact cleared", len(g.snapshot))
	return nil
}

// emitConflictWarning prints a framed, unmistakable warning block covering the
// full transactional outcome: commit succeeded, some unstaged worktree state could
// not be auto-restored, where the recovery artifact is, which paths are affected,
// and that manual reconciliation is required. Each line is logged on the dedicated
// guard stream so callers can route/elevate it. Caller holds g.mu.
func (g *worktreeGuard) emitConflictWarning(conflicts []string) {
	sort.Strings(conflicts)
	const bar = "════════════════════════════════════════════════════════════════════"
	lines := []string{
		bar,
		"⚠  WORKTREE PRESERVATION CONFLICT — MANUAL RECONCILIATION REQUIRED",
		bar,
		"The commit SUCCEEDED and is recorded.",
		"",
		fmt.Sprintf("However, %d unstaged worktree path(s) were modified during hook", len(conflicts)),
		"execution to content that is neither your pre-hook edit nor a clean reset,",
		"so StageFreight did NOT overwrite them (doing so would itself destroy work).",
		"",
		"Your current on-disk content is INTACT and untouched. Your pre-hook unstaged",
		"content is preserved verbatim in the recovery artifact below — nothing was lost,",
		"but the two must be reconciled by hand.",
		"",
		"  Affected path(s):",
	}
	for _, p := range conflicts {
		lines = append(lines, "    • "+p)
	}
	lines = append(lines,
		"",
		"  Recovery artifact (JSON; base64 `content` per path):",
		"    "+g.artifactPath,
		"",
		"  Next steps:",
		"    1. Compare your on-disk files against the artifact's preserved content.",
		"    2. Merge in any pre-hook work you still want.",
		"    3. Delete the artifact once reconciled.",
		bar,
	)
	for _, ln := range lines {
		g.log(guardLogStream, ln)
	}
}

// matchesBaseline reports whether cur is exactly the index baseline blob — the
// content a pre-commit stash/reset reverts an unstaged change to. A zero baseline
// (no index entry) never matches, so such paths fall through to conflict handling.
func (g *worktreeGuard) matchesBaseline(f guardedFile, cur []byte) bool {
	if f.baseline.IsZero() {
		return false
	}
	return plumbing.ComputeHash(plumbing.BlobObject, cur) == f.baseline
}

func (g *worktreeGuard) writeFile(full string, f guardedFile) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Errorf("%s: mkdir: %v", f.path, err)
	}
	mode := f.mode
	if mode == 0 {
		mode = 0o644
	}
	if err := os.WriteFile(full, f.content, mode); err != nil {
		return fmt.Errorf("%s: %v", f.path, err)
	}
	return nil
}

// stopSignalWatch tears down the signal handler exactly once. Caller holds g.mu.
// signal.Stop drains the registration; the goroutine then observes done and
// returns. A signal racing in after Stop is harmless: a fresh receive would find
// finalized==true and skip restore, doing only the (idempotent) re-raise.
func (g *worktreeGuard) stopSignalWatch() {
	if g.watchStopped {
		return
	}
	if g.sigCh != nil {
		signal.Stop(g.sigCh)
	}
	close(g.done)
	g.watchStopped = true
}

func (g *worktreeGuard) removeArtifact() {
	if g.artifactPath == "" {
		return
	}
	if err := os.Remove(g.artifactPath); err != nil && !os.IsNotExist(err) {
		g.logf("warning: could not remove recovery artifact %s: %v", g.artifactPath, err)
	}
}

func (g *worktreeGuard) logf(format string, a ...any) {
	g.log(guardLogStream, fmt.Sprintf(format, a...))
}

// surfaceOrphanedSnapshots reports recovery artifacts left by a prior hard kill
// (snapshot persisted, restore never ran). It only SURFACES them — it does not
// auto-restore, because a stale snapshot could clobber newer work; the invariant
// for that edge is discoverability. The artifacts are JSON snapshots (path +
// base64 content + index baseline), not git patches — the message reflects that.
func surfaceOrphanedSnapshots(repoRoot string, log func(stream, line string)) {
	dir := snapshotArtifactDir(repoRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no dir → nothing orphaned
	}
	logf := logOrNoop(log)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "snapshot-") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		var art snapshotArtifact
		if data, rerr := os.ReadFile(full); rerr == nil {
			_ = json.Unmarshal(data, &art)
		}
		paths := make([]string, 0, len(art.Files))
		for _, f := range art.Files {
			paths = append(paths, f.Path)
		}
		logf(guardLogStream, fmt.Sprintf(
			"orphaned worktree recovery snapshot from a prior interrupted run: %s (created %s). "+
				"It is a JSON snapshot holding the pre-hook content of %d path(s): %s. "+
				"StageFreight did not auto-apply it (it may conflict with current work) — "+
				"inspect the file, restore the needed paths from its base64 `content` fields, then delete it.",
			full, art.CreatedAt, len(art.Files), strings.Join(paths, ", ")))
	}
}

func logOrNoop(f func(stream, line string)) func(stream, line string) {
	if f == nil {
		return func(stream, line string) {}
	}
	return f
}
