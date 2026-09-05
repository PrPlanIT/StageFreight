package commit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/workspace"
)

// GitBackend executes commits via go-git — no git binary required.
// Push/sync is also handled via go-git through the Engine.
type GitBackend struct {
	RootDir string
	// Cfg carries the forge graph so a push authenticates HOST-BOUND — only with the
	// credential of the forge that owns the remote's host. nil ⇒ anonymous over HTTPS
	// (system git still handles local auth), never a mismatched token.
	Cfg *config.Config
	// OnCommitLine is called for each output line during hook execution and sync
	// transition events. stream: "stdout", "stderr", "hook_side_effect", "sync".
	// If nil, output is captured but not forwarded.
	OnCommitLine func(stream string, line string)
}

// Execute stages files, creates a commit, and optionally pushes.
func (g *GitBackend) Execute(_ context.Context, plan *Plan, conventional bool) (*Result, error) {
	return g.executeViaEngine(plan, conventional)
}

// executeViaEngine creates a commit using pure go-git — no git binary required.
//
// Named returns are used so the deferred worktree-preservation guard can surface
// a restore failure as the function's error when nothing else has already failed.
func (g *GitBackend) executeViaEngine(plan *Plan, conventional bool) (result *Result, err error) {
	// Surface any recovery artifact left behind by a prior interrupted run before
	// doing anything else — discoverability of stranded worktree state is an
	// invariant even when this run cannot auto-restore it.
	surfaceOrphanedSnapshots(g.RootDir, g.OnCommitLine)

	repo, err := gitstate.OpenRepo(g.RootDir)
	if err != nil {
		return nil, fmt.Errorf("opening repo: %w", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("opening worktree: %w", err)
	}

	// 0. Ensure the StageFreight namespace ignore file is present and current before
	// staging. This is what makes the "ephemeral ignored, durable committed" guarantee
	// UNIVERSAL: SF plants a self-contained .stagefreight/.gitignore in every repo it
	// commits to, so no project needs a hand-edited root .gitignore. Stage it only when
	// it changed, so ordinary commits are untouched and the file rides along exactly when
	// it is first created or its durable allowlist changes.
	if changed, werr := workspace.EnsureGitignore(g.RootDir); werr != nil {
		return nil, fmt.Errorf("ensuring namespace gitignore: %w", werr)
	} else if changed {
		if _, aerr := wt.Add(filepath.Join(workspace.NamespaceDir, ".gitignore")); aerr != nil {
			return nil, fmt.Errorf("staging namespace gitignore: %w", aerr)
		}
	}

	// 1. Stage files
	switch plan.StageMode {
	case StageExplicit, StageScoped:
		for _, p := range plan.Paths {
			if _, err := wt.Add(p); err != nil {
				return nil, fmt.Errorf("staging %s: %w", p, err)
			}
		}
	case StageAll:
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			return nil, fmt.Errorf("staging all: %w", err)
		}
	case StageStaged:
		// nothing — use whatever is already staged
	}

	// 2. Read staged files
	status, err := wt.Status()
	if err != nil {
		return nil, fmt.Errorf("reading worktree status: %w", err)
	}
	files := gitstate.StagedFiles(status)

	// 2a. Prove the declared paths actually landed. Staging is silent about paths it
	// drops (most often because an ancestor .gitignore excludes them — git cannot
	// re-include a path whose PARENT directory is ignored, which voids the namespace
	// allowlist planted above). Without this check the commit "succeeds" while the
	// content it was created to publish is missing — e.g. scribe committing a README
	// whose badge SVGs were never added, so every rendered badge 404s.
	if plan.StageMode == StageExplicit || plan.StageMode == StageScoped {
		if err := assertDeclaredPathsStaged(repo, g.RootDir, plan.Paths, files); err != nil {
			return nil, err
		}
	}

	// 2b. A scoped commit contains only the paths it named.
	if plan.StageMode == StageScoped {
		if err := assertNoUndeclaredStaged(plan.Paths, files); err != nil {
			return nil, err
		}
	}

	// 3. No-op check
	nothingToCommit := len(files) == 0
	if nothingToCommit && !plan.Push.Enabled {
		return &Result{NoOp: true}, nil
	}

	result = &Result{Backend: "go-git", NoOp: nothingToCommit}

	if !nothingToCommit {
		// 4. Resolve author identity: local config → global config → built-in defaults
		sig := resolveAuthorSignature(repo)

		// 5. Build full commit message including SF trailer
		msg := plan.Message(conventional)
		if plan.SignOff {
			msg += "\n\nSigned-off-by: " + sig.Name + " <" + sig.Email + ">"
		}

		// Transactional worktree preservation. Snapshot the operator's unstaged
		// tracked changes that are NOT part of this commit BEFORE any hook can
		// stash/wipe them, and restore on every exit path: the defer below covers
		// normal and hook-failure exits, and the guard arms a SIGINT/SIGTERM
		// handler for graceful interrupts. This brings the commit path to the same
		// preservation bar the Replay path already meets.
		guard, gerr := captureWorktreeGuard(repo, wt, g.RootDir, g.OnCommitLine)
		if gerr != nil {
			return nil, fmt.Errorf("worktree preservation guard: %w", gerr)
		}
		defer func() {
			if cerr := guard.Close(); cerr != nil && err == nil {
				err = cerr
			}
		}()

		// 6. pre-commit hook — abort on non-zero exit
		if err := RunPreCommitHook(g.RootDir, wt, g.OnCommitLine); err != nil {
			return nil, fmt.Errorf("pre-commit hook: %w", err)
		}

		// 7. commit-msg hook — hook may modify the message; re-read after
		msg, err = RunCommitMsgHook(g.RootDir, msg, g.OnCommitLine)
		if err != nil {
			return nil, fmt.Errorf("commit-msg hook: %w", err)
		}

		// 8. Create commit
		hash, err := wt.Commit(msg, &git.CommitOptions{
			Author:    &sig,
			Committer: &sig,
		})
		if err != nil {
			return nil, fmt.Errorf("committing: %w", err)
		}

		// 9. post-commit hook — non-zero exit is a warning, not an abort
		RunPostCommitHook(g.RootDir, g.OnCommitLine)

		result.SHA = hash.String()
		result.Message = msg
		result.Files = files
	}

	// 10. Push via the unified push entry point — runs even when nothing was committed
	if plan.Push.Enabled {
		syncResult, err := g.Push(plan.Push)
		if err != nil {
			return nil, fmt.Errorf("push: %w", err)
		}
		result.Sync = syncResult
		result.Pushed = containsAction(syncResult.ActionsExecuted, SyncPush)
	}

	return result, nil
}

// resolveAuthorSignature reads user.name and user.email from git config.
// Resolution order: local repo config → global config → built-in defaults.
func resolveAuthorSignature(repo *git.Repository) object.Signature {
	name, email := "stagefreight", "stagefreight@localhost"

	if cfg, err := repo.Config(); err == nil {
		if cfg.User.Name != "" {
			name = cfg.User.Name
		}
		if cfg.User.Email != "" {
			email = cfg.User.Email
		}
	}

	// Fall back to global config when local has no user identity configured
	if name == "stagefreight" || email == "stagefreight@localhost" {
		if global, err := gitconfig.LoadConfig(gitconfig.GlobalScope); err == nil {
			if global.User.Name != "" && name == "stagefreight" {
				name = global.User.Name
			}
			if global.User.Email != "" && email == "stagefreight@localhost" {
				email = global.User.Email
			}
		}
	}

	return object.Signature{Name: name, Email: email, When: time.Now()}
}

// BranchFromRefspec extracts the branch name from a refspec like "HEAD:refs/heads/main".
func BranchFromRefspec(refspec string) string {
	if idx := strings.LastIndex(refspec, "refs/heads/"); idx >= 0 {
		return refspec[idx+len("refs/heads/"):]
	}
	return ""
}

// assertDeclaredPathsStaged verifies every explicitly declared path contributed to the
// commit — either by staging a change, or by already being tracked and unchanged.
//
// A declared path that exists on disk with content, yet is neither staged nor tracked,
// was DROPPED by staging. go-git (like git) applies ignore rules while walking a
// declared directory and says nothing about what it skipped, so this is the only place
// the loss is observable. Failing here converts a silent, corrupt success — a commit
// that publishes references to files it never committed — into a loud, precise error.
func assertDeclaredPathsStaged(repo *git.Repository, rootDir string, declared []string, staged []string) error {
	if len(declared) == 0 {
		return nil
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return fmt.Errorf("reading index: %w", err)
	}
	tracked := make(map[string]bool, len(idx.Entries))
	for _, e := range idx.Entries {
		tracked[filepath.ToSlash(e.Name)] = true
	}
	stagedSet := make(map[string]bool, len(staged))
	for _, s := range staged {
		stagedSet[filepath.ToSlash(s)] = true
	}
	covered := func(rel string) bool {
		rel = filepath.ToSlash(rel)
		if stagedSet[rel] || tracked[rel] {
			return true
		}
		for s := range stagedSet {
			if strings.HasPrefix(s, rel+"/") {
				return true
			}
		}
		for t := range tracked {
			if strings.HasPrefix(t, rel+"/") {
				return true
			}
		}
		return false
	}

	for _, p := range declared {
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(p), "./"))
		abs := filepath.Join(rootDir, filepath.FromSlash(rel))
		info, statErr := os.Stat(abs)
		if statErr != nil {
			continue // declared but absent — nothing was produced, not a silent drop
		}
		if info.IsDir() && dirIsEmpty(abs) {
			continue // nothing to contribute
		}
		if covered(rel) {
			continue
		}
		return fmt.Errorf("commit: declared path %q exists but was not staged or tracked — "+
			"staging dropped it, almost always because a .gitignore rule excludes it or an "+
			"ancestor directory of it (git cannot re-include a path whose parent directory is "+
			"ignored, which voids %s/.gitignore's durable allowlist). Committing would publish "+
			"references to content that is not in the repository",
			rel, workspace.NamespaceDir)
	}
	return nil
}

// dirIsEmpty reports whether dir contains no entries at all.
func dirIsEmpty(dir string) bool {
	ents, err := os.ReadDir(dir)
	return err != nil || len(ents) == 0
}

// assertNoUndeclaredStaged fails when the index holds staged paths outside the declared
// set, naming them. A declared directory covers everything beneath it.
func assertNoUndeclaredStaged(declared []string, staged []string) error {
	declaredSet := make(map[string]bool, len(declared))
	for _, d := range declared {
		declaredSet[filepath.ToSlash(d)] = true
	}
	covered := func(rel string) bool {
		rel = filepath.ToSlash(rel)
		if declaredSet[rel] {
			return true
		}
		for d := range declaredSet {
			if strings.HasPrefix(rel, d+"/") {
				return true
			}
		}
		return false
	}

	// StageFreight plants and stages its own namespace ignore file on every commit, so it
	// is never something the caller failed to name.
	planted := filepath.ToSlash(filepath.Join(workspace.NamespaceDir, ".gitignore"))

	var undeclared []string
	for _, s := range staged {
		if filepath.ToSlash(s) == planted {
			continue
		}
		if !covered(s) {
			undeclared = append(undeclared, filepath.ToSlash(s))
		}
	}
	if len(undeclared) == 0 {
		return nil
	}
	sort.Strings(undeclared)
	return fmt.Errorf(
		"refusing to commit %d staged path(s) this commit did not name: %s\n"+
			"`-- <paths>` bounds the commit to what it lists. To include them say so with --add, "+
			"or unstage them (git restore --staged <path>)",
		len(undeclared), strings.Join(undeclared, ", "))
}
