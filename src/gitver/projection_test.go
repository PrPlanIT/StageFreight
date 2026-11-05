package gitver

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type testRepo struct {
	t    *testing.T
	dir  string
	repo *git.Repository
	wt   *git.Worktree
	n    int
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	t.Setenv("CI_COMMIT_TAG", "")
	t.Setenv("SF_CI_BRANCH", "")
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	return &testRepo{t: t, dir: dir, repo: repo, wt: wt}
}

func (r *testRepo) commit(msg string, parents ...plumbing.Hash) plumbing.Hash {
	r.t.Helper()
	r.n++
	name := fmt.Sprintf("f%d", r.n)
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(msg), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.wt.Add(name); err != nil {
		r.t.Fatal(err)
	}
	h, err := r.wt.Commit(msg, &git.CommitOptions{
		Author:  &object.Signature{Name: "t", Email: "t@t", When: time.Unix(int64(1_700_000_000+r.n), 0)},
		Parents: parents,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return h
}

func (r *testRepo) tag(name string, h plumbing.Hash) {
	r.t.Helper()
	if _, err := r.repo.CreateTag(name, h, nil); err != nil {
		r.t.Fatal(err)
	}
}

func (r *testRepo) version(majors string) string {
	r.t.Helper()
	v, err := DetectVersionWithOpts(r.dir, &VersioningOpts{
		TagSources: []TagSource{
			{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`},
			{ID: "prerelease", Pattern: `^v?\d+\.\d+\.\d+-.+$`},
		},
		BranchRules:   []BranchRule{{ID: "default", IsDefault: true, BaseFromIDs: []string{"stable"}}},
		NoLineageMode: "error",
		Majors:        majors,
	})
	if err != nil {
		r.t.Fatalf("DetectVersionWithOpts: %v", err)
	}
	return v.Version
}

func short(h plumbing.Hash) string { return h.String()[:7] }

// A build is versioned as the release it leads to, and only ever climbs.
func TestProjection_NextReleaseAndClimb(t *testing.T) {
	r := newTestRepo(t)
	r.tag("v0.15.0", r.commit("feat: the release"))

	h := r.commit("fix: a fix")
	if got, want := r.version(""), "0.15.1-dev.1+"+short(h); got != want {
		t.Errorf("fix: got %s, want %s", got, want)
	}
	h = r.commit("docs: notes")
	if got, want := r.version(""), "0.15.1-dev.2+"+short(h); got != want {
		t.Errorf("docs: got %s, want %s", got, want)
	}
	h = r.commit("feat(api): a feature")
	if got, want := r.version(""), "0.16.0-dev.3+"+short(h); got != want {
		t.Errorf("feat: got %s, want %s", got, want)
	}
}

func TestProjection_Majors(t *testing.T) {
	for _, tc := range []struct{ base, majors, want string }{
		{"v0.15.0", "", "0.16.0"},
		{"v0.15.0", MajorsAfterV1, "0.16.0"},
		{"v0.15.0", MajorsAlways, "1.0.0"},
		{"v0.15.0", MajorsNever, "0.16.0"},
		{"v1.2.3", MajorsAfterV1, "2.0.0"},
		{"v1.2.3", MajorsAlways, "2.0.0"},
		{"v1.2.3", MajorsNever, "1.3.0"},
	} {
		r := newTestRepo(t)
		r.tag(tc.base, r.commit("chore: base"))
		h := r.commit("refactor!: drop the old api")
		if got, want := r.version(tc.majors), tc.want+"-dev.1+"+short(h); got != want {
			t.Errorf("%s majors=%q: got %s, want %s", tc.base, tc.majors, got, want)
		}
	}
}

// Commits merged in from a branch count once; the release's own history never counts.
func TestProjection_MergeHistory(t *testing.T) {
	r := newTestRepo(t)
	root := r.commit("chore: root")
	r.tag("v1.0.0", root)
	side := r.commit("feat: on a branch", root)
	mainTip := r.commit("fix: on main", root)
	merge := r.commit("Merge branch 'feature'", mainTip, side)
	if got, want := r.version(""), "1.1.0-dev.3+"+short(merge); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// A prerelease base already names the release it leads to.
func TestProjection_PrereleaseBase(t *testing.T) {
	r := newTestRepo(t)
	r.tag("v0.16.0-rc.1", r.commit("feat: candidate"))
	h := r.commit("fix: a fix")
	v, err := DetectVersionWithOpts(r.dir, &VersioningOpts{
		TagSources:    []TagSource{{ID: "prerelease", Pattern: `^v?\d+\.\d+\.\d+-.+$`}},
		BranchRules:   []BranchRule{{ID: "default", IsDefault: true, BaseFromIDs: []string{"prerelease"}}},
		NoLineageMode: "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "0.16.0-dev.1+" + short(h); v.Version != want {
		t.Errorf("got %s, want %s", v.Version, want)
	}
}

// A format without {next}/{commits} renders as declared.
func TestProjection_BaseFormatUnchanged(t *testing.T) {
	r := newTestRepo(t)
	r.tag("v0.15.0", r.commit("feat: the release"))
	h := r.commit("feat: more")
	v, err := DetectVersionWithOpts(r.dir, &VersioningOpts{
		TagSources:    []TagSource{{ID: "stable", Pattern: `^v?\d+\.\d+\.\d+$`}},
		BranchRules:   []BranchRule{{ID: "default", IsDefault: true, BaseFromIDs: []string{"stable"}, Format: "{base}-dev+{sha}"}},
		NoLineageMode: "error",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "0.15.0-dev+" + short(h); v.Version != want {
		t.Errorf("got %s, want %s", v.Version, want)
	}
}

func TestParseConventional(t *testing.T) {
	for msg, want := range map[string]Conventional{
		"feat(api)!: drop v1":                         {Type: "feat", Scope: "api", Description: "drop v1", Breaking: true},
		"fix: x\n\nBREAKING CHANGE: the flag is gone": {Type: "fix", Description: "x", Body: "BREAKING CHANGE: the flag is gone", Breaking: true},
		"fix: x\n\nBREAKING-CHANGE: gone":             {Type: "fix", Description: "x", Body: "BREAKING-CHANGE: gone", Breaking: true},
		"fix: x\n\nThis is not a breaking change":     {Type: "fix", Description: "x", Body: "This is not a breaking change"},
		"Merge branch 'feature'":                      {Description: "Merge branch 'feature'"},
	} {
		if got := ParseConventional(msg); got != want {
			t.Errorf("ParseConventional(%q) = %+v, want %+v", msg, got, want)
		}
	}
}
