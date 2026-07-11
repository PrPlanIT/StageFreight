// Package workspace owns the .stagefreight/ generated namespace and keeps it
// hygienic. StageFreight writes run outputs there every execution; if any are
// committed they dirty the worktree and poison StageFreight's own clean-tree
// invariants. This package classifies that namespace — ephemeral run outputs are
// gitignored and never tracked, while persistent published assets (badges/) stay
// trackable — and provides deterministic, reversible normalization.
//
// Hard scope boundary: every mutation here is confined to .stagefreight/. It
// never edits the root .gitignore, never untracks files outside the namespace,
// and never touches user source trees.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/gitstate"
	"github.com/PrPlanIT/StageFreight/src/layout"
)

// NamespaceDir is the StageFreight-owned generated namespace. It is the Durable-bucket
// root; layout is the single source of truth (see src/layout).
const NamespaceDir = layout.Root

// ephemeralEntries are run-generated outputs under .stagefreight/ that must NOT
// be version controlled — rewritten every run. Directory entries end with "/".
var ephemeralEntries = []string{
	"deps/",
	"reports/",
	"security/",
	"dist/",
	"pipeline.json",
}

// persistentEntries are deliberately trackable — published assets referenced
// from the README. They are carved OUT of the ignore set and the clean-tree
// exclusion, so they keep their normal version-controlled behavior.
var persistentEntries = []string{
	"badges/",
}

// GitignorePath is the managed ignore file — INSIDE the namespace, never root.
func GitignorePath(rootDir string) string {
	return filepath.Join(rootDir, NamespaceDir, ".gitignore")
}

// gitignoreManaged is the full managed body of .stagefreight/.gitignore. Paths
// are anchored ("/") relative to the file's own directory (.stagefreight/).
func gitignoreManaged() string {
	var b strings.Builder
	b.WriteString("# Managed by StageFreight — do not edit.\n")
	b.WriteString("# Ephemeral run outputs; persistent assets (badges/) stay tracked.\n")
	for _, e := range ephemeralEntries {
		b.WriteString("/" + e + "\n")
	}
	return b.String()
}

// IsEphemeral reports whether a repo-relative path is a StageFreight-owned
// ephemeral output. Used both to detect tracked artifacts and to exclude
// StageFreight's own generated files from clean-tree gates (layer C). The
// persistent carve-out always wins.
func IsEphemeral(relPath string) bool {
	rel := filepath.ToSlash(relPath)
	prefix := NamespaceDir + "/"
	if !strings.HasPrefix(rel, prefix) {
		return false
	}
	tail := strings.TrimPrefix(rel, prefix)
	for _, p := range persistentEntries {
		if strings.HasPrefix(tail, p) {
			return false
		}
	}
	for _, e := range ephemeralEntries {
		if strings.HasSuffix(e, "/") {
			if strings.HasPrefix(tail, e) {
				return true
			}
		} else if tail == e {
			return true
		}
	}
	return false
}

// EnsureGitignore writes/updates .stagefreight/.gitignore to the managed body.
// Returns true if it created or changed the file. Touches ONLY that file.
func EnsureGitignore(rootDir string) (bool, error) {
	path := GitignorePath(rootDir)
	want := gitignoreManaged()
	if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// DetectTrackedEphemeral returns the repo-relative paths of tracked files (from
// the git index) that are StageFreight-owned ephemeral outputs.
func DetectTrackedEphemeral(rootDir string) ([]string, error) {
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return nil, err
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range idx.Entries {
		if IsEphemeral(e.Name) {
			out = append(out, e.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// UntrackEphemeral removes the given paths from the git index (the `git rm
// --cached` equivalent) WITHOUT touching the working-tree files — contents are
// preserved, only index ownership changes. It refuses any path that is not a
// StageFreight-owned ephemeral output: the hard scope boundary, enforced in code.
func UntrackEphemeral(rootDir string, paths []string) error {
	for _, p := range paths {
		if !IsEphemeral(p) {
			return fmt.Errorf("refusing to untrack %q: outside the StageFreight ephemeral namespace", p)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	repo, err := gitstate.OpenRepo(rootDir)
	if err != nil {
		return err
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return err
	}
	remove := make(map[string]bool, len(paths))
	for _, p := range paths {
		remove[filepath.ToSlash(p)] = true
	}
	kept := idx.Entries[:0]
	for _, e := range idx.Entries {
		if !remove[filepath.ToSlash(e.Name)] {
			kept = append(kept, e)
		}
	}
	idx.Entries = kept
	return repo.Storer.SetIndex(idx)
}
