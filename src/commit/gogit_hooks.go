package commit

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
)

// RunHook executes a git hook if it exists and is executable.
// Returns ErrHookRejected if the hook exits non-zero.
// cb receives incremental stdout/stderr lines — never silently swallowed.
//
// Hook sequence (caller's responsibility to run in order):
//  1. pre-commit — no args, stdin /dev/null. Non-zero → abort.
//  2. commit-msg <tmpfile> — write message to tmpfile, pass path as arg.
//     Hook may modify the file; re-read after. Non-zero → abort.
//  3. wt.Commit() — called by the CommitEngine, not here.
//  4. post-commit — no args. Non-zero → warning only, no abort.
//
// Hooks are treated as untrusted side-effect producers:
// - stdout/stderr always surfaced via cb
// - worktree snapshot before/after pre-commit; any new dirty paths emitted as hook_side_effect
// - filesystem mutations outside commit-msg tmpfile are detected and reported
func RunHook(repoRoot, hookName string, args []string, input []byte, cb func(stream, line string)) error {
	hookPath := filepath.Join(hooksDir(repoRoot), hookName)
	if !isExecutable(hookPath) {
		return nil // hook absent or not executable — skip silently
	}

	cmdArgs := append([]string{hookPath}, args...)
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...) //nolint:gosec — hook path is the repo's configured hooks directory, not user input
	cmd.Dir = repoRoot

	if len(input) > 0 {
		cmd.Stdin = bytes.NewReader(input)
	} else {
		cmd.Stdin, _ = os.Open(os.DevNull)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("hook %s stdout pipe: %w", hookName, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("hook %s stderr pipe: %w", hookName, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting hook %s: %w", hookName, err)
	}

	var wg sync.WaitGroup
	drainPipe := func(r io.Reader, stream string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(r)
			// Allow up to 1 MiB per line — hooks (e.g. linters) can produce long output
			scanner.Buffer(make([]byte, 1024), 1024*1024)
			for scanner.Scan() {
				if cb != nil {
					cb(stream, scanner.Text())
				}
			}
		}()
	}
	drainPipe(stdoutPipe, "stdout")
	drainPipe(stderrPipe, "stderr")
	wg.Wait()

	waitErr := cmd.Wait()
	if waitErr != nil {
		exitCode := 1
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return &gitstate.ErrHookRejected{Hook: hookName, ExitCode: exitCode}
	}
	return nil
}

// RunPreCommitHook executes the pre-commit hook and snapshots worktree
// state before/after, emitting hook_side_effect diagnostics for new dirty paths.
func RunPreCommitHook(repoRoot string, wt *git.Worktree, cb func(stream, line string)) error {
	// Snapshot status before hook
	statusBefore, _ := wt.Status()

	if err := RunHook(repoRoot, "pre-commit", nil, nil, cb); err != nil {
		return err
	}

	// Snapshot status after hook — report any new dirty paths as side effects
	statusAfter, _ := wt.Status()
	for path, fs := range statusAfter {
		before, existed := statusBefore[path]
		if !existed || (before.Staging == git.Unmodified && before.Worktree == git.Unmodified) {
			if fs.Staging != git.Unmodified || fs.Worktree != git.Unmodified {
				if cb != nil {
					cb("hook_side_effect", fmt.Sprintf("pre-commit hook modified: %s", path))
				}
			}
		}
	}
	return nil
}

// RunCommitMsgHook writes the message to a temp file, executes the commit-msg hook,
// and re-reads the (possibly modified) message from the file.
// Returns the (possibly modified) message and any error.
func RunCommitMsgHook(repoRoot, message string, cb func(stream, line string)) (string, error) {
	hookPath := filepath.Join(hooksDir(repoRoot), "commit-msg")
	if !isExecutable(hookPath) {
		return message, nil
	}

	// Write message to temp file in the git directory to match git's behaviour
	gitDir, _ := gitDirs(repoRoot)
	tmpFile, err := os.CreateTemp(gitDir, "COMMIT_EDITMSG.*")
	if err != nil {
		return message, fmt.Errorf("creating commit-msg tmpfile: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(message); err != nil {
		tmpFile.Close()
		return message, fmt.Errorf("writing commit message to tmpfile: %w", err)
	}
	tmpFile.Close()

	if err := RunHook(repoRoot, "commit-msg", []string{tmpPath}, nil, cb); err != nil {
		return message, err
	}

	// Re-read: hook may have modified the message
	modified, err := os.ReadFile(tmpPath)
	if err != nil {
		return message, fmt.Errorf("re-reading commit-msg tmpfile: %w", err)
	}
	return string(modified), nil
}

// RunPostCommitHook executes the post-commit hook.
// Non-zero exit is logged as a warning via cb but does NOT abort.
func RunPostCommitHook(repoRoot string, cb func(stream, line string)) {
	if err := RunHook(repoRoot, "post-commit", nil, nil, cb); err != nil {
		if cb != nil {
			cb("stderr", fmt.Sprintf("post-commit hook failed (ignored): %v", err))
		}
	}
}

// isExecutable returns true when path exists and has the executable bit set.
func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode()&0o111 != 0
}

// hooksDir is where git itself looks for this repository's hooks: core.hooksPath when
// set (repository, global or system config; "~/" expands to the home directory and a
// relative path is taken from the worktree root), otherwise the hooks directory of the
// shared git directory - which in a linked worktree is not <root>/.git/hooks, since
// there .git is a file pointing elsewhere.
func hooksDir(repoRoot string) string {
	if p := configuredHooksPath(repoRoot); p != "" {
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			if home, err := os.UserHomeDir(); err == nil {
				p = filepath.Join(home, rest)
			}
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, p)
		}
		return p
	}
	_, common := gitDirs(repoRoot)
	return filepath.Join(common, "hooks")
}

// configuredHooksPath reads core.hooksPath with git's precedence: repository, then
// global, then system - the first level that sets it wins.
func configuredHooksPath(repoRoot string) string {
	if repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{EnableDotGitCommonDir: true}); err == nil {
		if cfg, err := repo.Config(); err == nil {
			if p := cfg.Raw.Section("core").Option("hooksPath"); p != "" {
				return p
			}
		}
	}
	for _, scope := range []gitconfig.Scope{gitconfig.GlobalScope, gitconfig.SystemScope} {
		if cfg, err := gitconfig.LoadConfig(scope); err == nil {
			if p := cfg.Raw.Section("core").Option("hooksPath"); p != "" {
				return p
			}
		}
	}
	return ""
}

// gitDirs resolves a worktree's git directory and the shared (common) git directory.
// For a plain checkout both are <root>/.git; a linked worktree's .git is a file naming
// its own git directory, whose "commondir" file points at the shared one.
func gitDirs(repoRoot string) (gitDir, commonDir string) {
	dotGit := filepath.Join(repoRoot, ".git")
	fi, err := os.Stat(dotGit)
	if err != nil || fi.IsDir() {
		return dotGit, dotGit
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit, dotGit
	}
	gitDir = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repoRoot, gitDir)
	}
	commonDir = gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(c))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	return gitDir, filepath.Clean(commonDir)
}
