package render

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// ForgeTarget returns the CI file path that a forge expects.
func ForgeTarget(forge string) (string, error) {
	switch forge {
	case "gitlab":
		return ".gitlab-ci.yml", nil
	case "github":
		return ".github/workflows/stagefreight.yml", nil
	case "gitea":
		return ".gitea/workflows/stagefreight.yml", nil
	case "forgejo":
		return ".forgejo/workflows/stagefreight.yml", nil
	default:
		return "", fmt.Errorf("unsupported forge %q", forge)
	}
}

// Check verifies the committed CI file matches the rendered output.
// Returns nil if current, a structured error describing the drift if stale.
func Check(rootDir, forge string, rendered []byte) error {
	target, err := ForgeTarget(forge)
	if err != nil {
		return err
	}

	path := filepath.Join(rootDir, target)
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("CI file missing: %s does not exist\n\nFix:\n  stagefreight ci render %s --write\n  git add %s\n  git commit", target, forge, target)
		}
		return fmt.Errorf("reading %s: %w", target, err)
	}

	if !bytes.Equal(existing, rendered) {
		return fmt.Errorf("CI is stale: %s does not match render output for forge=%s\n\nFix:\n  stagefreight ci render %s --write\n  git add %s\n  git commit", target, forge, forge, target)
	}

	return nil
}
