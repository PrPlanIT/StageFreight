package commit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sofmeright/stagefreight/src/forge"
)

// ForgeBackend creates commits purely via forge API (no local git commit).
type ForgeBackend struct {
	RootDir     string
	ForgeClient forge.Forge
	Branch      string
}

// Execute resolves changed files, reads their content, and commits via forge API.
func (f *ForgeBackend) Execute(ctx context.Context, plan *Plan, conventional bool) (*Result, error) {
	// 1. Resolve file list
	var changes []FileChange
	var err error
	switch plan.StageMode {
	case StageExplicit:
		for _, p := range plan.Paths {
			absPath := filepath.Join(f.RootDir, p)
			info, statErr := os.Stat(absPath)
			if os.IsNotExist(statErr) {
				changes = append(changes, FileChange{Path: p, Deleted: true})
				continue
			}
			if statErr != nil {
				return nil, fmt.Errorf("stat %s: %w", p, statErr)
			}
			if info.IsDir() {
				dirChanges, dirErr := gitChangedFilesInDir(f.RootDir, p)
				if dirErr != nil {
					return nil, fmt.Errorf("expanding directory %s: %w", p, dirErr)
				}
				changes = append(changes, dirChanges...)
			} else {
				changes = append(changes, FileChange{Path: p})
			}
		}
	case StageAll:
		changes, err = gitChangedFiles(f.RootDir)
	case StageStaged:
		changes, err = gitStagedChanges(f.RootDir)
	}
	if err != nil {
		return nil, fmt.Errorf("resolving files: %w", err)
	}

	// 2. No-op check
	if len(changes) == 0 {
		return &Result{NoOp: true}, nil
	}

	// 3. Build actions
	actions := make([]forge.FileAction, 0, len(changes))
	fileNames := make([]string, 0, len(changes))
	for _, c := range changes {
		fa := forge.FileAction{Path: c.Path, Delete: c.Deleted}
		if !c.Deleted {
			content, readErr := os.ReadFile(filepath.Join(f.RootDir, c.Path))
			if readErr != nil {
				return nil, fmt.Errorf("reading %s: %w", c.Path, readErr)
			}
			fa.Content = content
		}
		actions = append(actions, fa)
		fileNames = append(fileNames, c.Path)
	}

	// 4. Resolve current branch head for optimistic concurrency (forge-native)
	expectedSHA, err := f.ForgeClient.BranchHeadSHA(ctx, f.Branch)
	if err != nil {
		return nil, fmt.Errorf("resolving branch head: %w", err)
	}

	// 5. Commit via forge API
	commitResult, err := f.ForgeClient.CommitFiles(ctx, forge.CommitFilesOptions{
		Branch:      f.Branch,
		Message:     plan.Message(conventional),
		Files:       actions,
		ExpectedSHA: expectedSHA,
	})
	if err != nil {
		return nil, fmt.Errorf("forge commit: %w", err)
	}

	shaShort := commitResult.SHA
	if len(shaShort) > 12 {
		shaShort = shaShort[:12]
	}
	fmt.Fprintf(os.Stderr, "commit: created remote commit %s via %s API\n",
		shaShort, f.ForgeClient.Provider())

	// 6. Return result with real remote SHA
	return &Result{
		SHA:     commitResult.SHA,
		Message: plan.Message(conventional),
		Files:   fileNames,
		Pushed:  true,
	}, nil
}

// FileChange represents a file with its change status.
type FileChange struct {
	Path    string
	Deleted bool
}

// parsePorcelainStatus parses NUL-delimited `git status --porcelain=v1 -z` output.
//
// Format: each record is "XY path\0" where XY is the two-column status.
// For renames/copies (X or Y is R/C), the record is "XY oldpath\0newpath\0"
// — the old path is embedded in the first record after the status prefix,
// and the new (destination) path follows as a separate NUL-delimited record.
// We use the new path since that's the file that exists in the working tree.
//
// Deduplicates by final path to handle rename+edit combos.
func parsePorcelainStatus(out []byte) []FileChange {
	if len(out) == 0 {
		return nil
	}
	seen := make(map[string]FileChange)
	var order []string
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if len(rec) < 4 {
			continue
		}
		status := rec[0:2]
		path := rec[3:]
		deleted := status[0] == 'D' || status[1] == 'D'
		// Renames/copies: next NUL-delimited record is the new (destination) path
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++
			if i < len(records) {
				path = records[i]
			}
		}
		if _, exists := seen[path]; !exists {
			order = append(order, path)
		}
		seen[path] = FileChange{Path: path, Deleted: deleted}
	}
	changes := make([]FileChange, 0, len(order))
	for _, p := range order {
		changes = append(changes, seen[p])
	}
	return changes
}

// gitChangedFiles returns all changed files with delete status.
func gitChangedFiles(rootDir string) ([]FileChange, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "-uall")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parsePorcelainStatus(out), nil
}

// gitChangedFilesInDir returns changed files within a specific directory.
func gitChangedFilesInDir(rootDir, dir string) ([]FileChange, error) {
	cmd := exec.Command("git", "status", "--porcelain=v1", "-z", "-uall", "--", dir)
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parsePorcelainStatus(out), nil
}

// gitStagedChanges returns staged files with delete status.
// Uses -z for NUL-delimited machine-safe output.
//
// Format with --name-status -z: records are NUL-separated as "status\0path\0".
// For renames/copies (status starts with R or C, e.g. "R100"), the sequence is
// "Rnnn\0oldpath\0newpath\0" — three NUL-delimited fields. We consume the old
// path and use the new path since that's what exists in the index.
func gitStagedChanges(rootDir string) ([]FileChange, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-status", "-z")
	cmd.Dir = rootDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	seen := make(map[string]FileChange)
	var order []string
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		status := records[i]
		if status == "" {
			continue
		}
		i++
		if i >= len(records) {
			break
		}
		path := records[i]
		deleted := len(status) > 0 && status[0] == 'D'
		// Renames/copies: status starts with R or C (e.g. "R100", "C050")
		if len(status) > 0 && (status[0] == 'R' || status[0] == 'C') {
			i++
			if i < len(records) {
				path = records[i]
			}
		}
		if _, exists := seen[path]; !exists {
			order = append(order, path)
		}
		seen[path] = FileChange{Path: path, Deleted: deleted}
	}
	changes := make([]FileChange, 0, len(order))
	for _, p := range order {
		changes = append(changes, seen[p])
	}
	return changes, nil
}
