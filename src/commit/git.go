package commit

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// GitBackend executes commits via the git CLI.
type GitBackend struct {
	RootDir string
}

// Execute stages files, creates a commit, and optionally pushes via git.
func (g *GitBackend) Execute(_ context.Context, plan *Plan, conventional bool) (*Result, error) {
	// 1. Stage files
	switch plan.StageMode {
	case StageExplicit:
		for _, p := range plan.Paths {
			if err := g.git("add", p); err != nil {
				return nil, fmt.Errorf("staging %s: %w", p, err)
			}
		}
	case StageAll:
		if err := g.git("add", "-A"); err != nil {
			return nil, fmt.Errorf("staging all: %w", err)
		}
	case StageStaged:
		// nothing — use whatever is already staged
	}

	// 2. Capture actual staged files
	files, err := gitStagedFiles(g.RootDir)
	if err != nil {
		return nil, fmt.Errorf("reading staged files: %w", err)
	}

	// 3. No-op check
	if len(files) == 0 {
		return &Result{NoOp: true}, nil
	}

	// 4. Ensure git author identity exists (CI images often lack it)
	g.ensureAuthorIdentity()

	// 5. Build commit command
	subject := plan.Subject(conventional)
	commitArgs := []string{"commit", "-m", subject}
	if plan.Body != "" {
		commitArgs = append(commitArgs, "-m", plan.Body)
	}
	if plan.SignOff {
		commitArgs = append(commitArgs, "--signoff")
	}

	if err := g.git(commitArgs...); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}

	// 6. Capture SHA
	sha, err := g.gitOutput("rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("reading commit SHA: %w", err)
	}

	result := &Result{
		SHA:     sha,
		Message: plan.Message(conventional),
		Files:   files,
	}

	// 7. Push
	if plan.Push.Enabled {
		pushArgs := []string{"push", plan.Push.Remote}
		if plan.Push.Refspec != "" {
			pushArgs = append(pushArgs, plan.Push.Refspec)
		}
		if err := g.git(pushArgs...); err != nil {
			return nil, fmt.Errorf("pushing: %w", err)
		}
		result.Pushed = true
	}

	return result, nil
}

// BranchFromRefspec extracts the branch name from a refspec like "HEAD:refs/heads/main".
func BranchFromRefspec(refspec string) string {
	if idx := strings.LastIndex(refspec, "refs/heads/"); idx >= 0 {
		return refspec[idx+len("refs/heads/"):]
	}
	return ""
}

// ensureAuthorIdentity sets repo-local git user.name and user.email if not already configured.
func (g *GitBackend) ensureAuthorIdentity() {
	if name, _ := g.gitOutput("config", "user.name"); name == "" {
		_ = g.git("config", "user.name", "stagefreight")
	}
	if email, _ := g.gitOutput("config", "user.email"); email == "" {
		_ = g.git("config", "user.email", "stagefreight@localhost")
	}
}

func (g *GitBackend) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RootDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (g *GitBackend) gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.RootDir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
