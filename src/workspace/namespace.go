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
	"github.com/PrPlanIT/StageFreight/src/paths"
)

// NamespaceDir is the StageFreight-owned generated namespace. It is the Durable-bucket
// root; paths is the single source of truth (see src/paths).
const NamespaceDir = paths.Root

// persistentEntries is the DURABLE allowlist — the only content under .stagefreight/
// that is committed. Everything else in the namespace is ephemeral run output. This is
// the small, stable side of the split, so enumerating it (rather than the growing set of
// ephemeral outputs) is what makes new outputs ignored by default. Directory entries end
// with "/". Adding a durable file here is what carves it out of the ignore set and the
// clean-tree exclusion; miss one and it is silently treated as ephemeral — hence the
// completeness test.
var persistentEntries = []string{
	"badges/",       // published status assets referenced from the README
	"preset-cache/", // resolved config presets, committed for reproducibility
	"toolchains.lock", // machine-maintained toolchain resolved-lock (durable state)
}

// GitignorePath is the managed ignore file — INSIDE the namespace, never root.
func GitignorePath(rootDir string) string {
	return filepath.Join(rootDir, NamespaceDir, ".gitignore")
}

// gitignoreManaged is the full managed body of .stagefreight/.gitignore. It is an
// ALLOWLIST: ignore everything under the namespace, then carve out the durable set —
// so a new ephemeral output is ignored by default and only a deliberate addition to
// persistentEntries makes something committable. Paths are anchored ("/") relative to
// the file's own directory (.stagefreight/). "/*" matches direct children only, so
// re-including a durable directory keeps its contents tracked.
func gitignoreManaged() string {
	var b strings.Builder
	b.WriteString("# Managed by StageFreight — do not edit.\n")
	b.WriteString("# Everything here is ephemeral run output and ignored, EXCEPT the\n")
	b.WriteString("# durable set carved out below (allowlist — new outputs stay ignored).\n")
	b.WriteString("/*\n")
	b.WriteString("!/.gitignore\n")
	for _, p := range persistentEntries {
		b.WriteString("!/" + p + "\n")
	}
	return b.String()
}

// IsEphemeral reports whether a repo-relative path is a StageFreight-owned
// ephemeral output: anything under the namespace that is NOT in the durable
// allowlist. Used both to detect tracked artifacts and to exclude StageFreight's
// own generated files from clean-tree gates (layer C). This is the inverse of the
// allowlist — an UNKNOWN output under .stagefreight/ is ephemeral by default (safe:
// a new output can never accidentally be treated as committable), and the durable
// carve-out (including the managed .gitignore) always wins.
func IsEphemeral(relPath string) bool {
	rel := filepath.ToSlash(relPath)
	prefix := NamespaceDir + "/"
	if !strings.HasPrefix(rel, prefix) {
		return false // outside the namespace — not ours
	}
	tail := strings.TrimPrefix(rel, prefix)
	if tail == ".gitignore" {
		return false // the managed ignore file is durable
	}
	for _, p := range persistentEntries {
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(tail, p) {
				return false // inside a durable directory
			}
		} else if tail == p {
			return false // a durable file
		}
	}
	return true // under the namespace, not durable → ephemeral
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
