package governance

import (
	"errors"
	"fmt"
	"testing"
)

type scriptedForge struct {
	files   map[string][]byte
	failing map[string]error
	commits int
}

func (f *scriptedForge) GetFileContent(_, path, _ string) ([]byte, error) {
	if err, ok := f.failing[path]; ok {
		return nil, err
	}
	if b, ok := f.files[path]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrFileNotFound, path)
}

func (f *scriptedForge) DefaultBranch(string) (string, error) { return "main", nil }

func (f *scriptedForge) CommitFiles(_, _, _ string, _ []FileCommit) (string, error) {
	f.commits++
	return "sha", nil
}

// A read that fails is not a file that is missing. Treating it as one made the forge
// write the existing content back unchanged: an empty commit per satellite whose read
// happened to fail during a reconcile.
func TestReadFailureNeverBecomesACommit(t *testing.T) {
	fg := &scriptedForge{
		files:   map[string][]byte{".stagefreight.yml": []byte("same\n")},
		failing: map[string]error{"README.md": errors.New("GET …/files/README.md: 429 Too Many Requests")},
	}

	if got := planFile(fg, "org/repo", "NEW.md", []byte("x")); got.Action != "create" {
		t.Errorf("a missing file is created: got %q", got.Action)
	}
	if got := planFile(fg, "org/repo", ".stagefreight.yml", []byte("same\n")); got.Action != "unchanged" {
		t.Errorf("identical content is left alone: got %q", got.Action)
	}

	plan := DistributionPlan{Repo: "org/repo", Files: []DistributedFile{
		planFile(fg, "org/repo", ".stagefreight.yml", []byte("changed\n")),
		planFile(fg, "org/repo", "README.md", []byte("readme\n")),
	}}
	res := commitRepo(plan, fg, "gitlab.example/Org/policy", false)
	if res.Status != "error" || fg.commits != 0 {
		t.Errorf("an unreadable file stops the repo's commit: status=%q commits=%d", res.Status, fg.commits)
	}
}
