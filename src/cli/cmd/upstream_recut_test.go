package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/release"
)

type fakeTagForge struct {
	deleteErr error
	createErr map[string]error // by ref
	deleted   []string
	created   []string // tag@ref
}

func (f *fakeTagForge) DeleteTag(_ context.Context, tag string) error {
	f.deleted = append(f.deleted, tag)
	return f.deleteErr
}

func (f *fakeTagForge) CreateTag(_ context.Context, tag, ref string) error {
	if err := f.createErr[ref]; err != nil {
		return err
	}
	f.created = append(f.created, tag+"@"+ref)
	return nil
}

// recutRepo commits files into a fresh repository, one commit per call to step.
func recutRepo(t *testing.T) (string, *git.Repository, func(msg string, files map[string]string) plumbing.Hash) {
	t.Helper()
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	step := func(msg string, files map[string]string) plumbing.Hash {
		for name, body := range files {
			p := filepath.Join(root, name)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Add(name); err != nil {
				t.Fatal(err)
			}
		}
		return mkCommit(t, repo, msg)
	}
	return root, repo, step
}

func TestBuildInputsDigest_DockerFollowsDockerignore(t *testing.T) {
	_, repo, step := recutRepo(t)
	docker := []config.BuildConfig{{ID: "image", Kind: "docker"}}
	base := step("base", map[string]string{
		"Dockerfile":    "FROM debian\nCOPY src /src\n",
		"src/run.sh":    "echo 1\n",
		"README.md":     "# app\n",
		".dockerignore": "README.md\ndocs\n",
	})
	digest := func(h plumbing.Hash) string {
		d, err := buildInputsDigest(repo, h, docker)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	want := digest(base)

	readme := step("docs", map[string]string{"README.md": "# app, badges\n", "docs/x.md": "x\n", ".stagefreight/state": "1\n"})
	if digest(readme) != want {
		t.Error("files .dockerignore excludes and .stagefreight/ are not build inputs")
	}
	src := step("change", map[string]string{"src/run.sh": "echo 2\n"})
	if digest(src) == want {
		t.Error("a context file the image is built from changed")
	}
	df := step("bump", map[string]string{"Dockerfile": "FROM debian:trixie\nCOPY src /src\n"})
	if digest(df) == digest(src) {
		t.Error("the Dockerfile changed")
	}
	ignoreDf := step("ignore the dockerfile", map[string]string{".dockerignore": "README.md\ndocs\nDockerfile\n"})
	if digest(ignoreDf) == digest(df) {
		t.Error(".dockerignore itself is a build input")
	}

	binary := []config.BuildConfig{{ID: "bin", Kind: "binary"}}
	a, _ := buildInputsDigest(repo, src, binary)
	b, _ := buildInputsDigest(repo, readme, binary)
	if a == b {
		t.Error("a binary build is made from the whole tree")
	}
}

func TestRecutPlan(t *testing.T) {
	root, repo, step := recutRepo(t)
	builds := []config.BuildConfig{{ID: "image", Kind: "docker"}}
	released := step("release", map[string]string{"Dockerfile": "FROM debian\n", ".dockerignore": "README.md\n", "README.md": "a\n"})
	if err := release.CreateAnnotatedTag(root, "v3.7.5", released.String(), "- release"); err != nil {
		t.Fatal(err)
	}

	if plan, why := recutPlan(root, builds, "v3.7.5", released.String()); plan != nil || why != "already released" {
		t.Errorf("the tagged commit itself: plan=%v why=%q", plan, why)
	}
	docs := step("docs", map[string]string{"README.md": "b\n"})
	if plan, _ := recutPlan(root, builds, "v3.7.5", docs.String()); plan != nil {
		t.Error("same build inputs: nothing to recut")
	}
	change := step("change", map[string]string{"Dockerfile": "FROM debian\nENV X=0\n"})
	plan, why := recutPlan(root, builds, "v3.7.5", change.String())
	if plan == nil || plan.from != released.String() || plan.to != change.String() {
		t.Fatalf("changed inputs recut: plan=%v why=%q", plan, why)
	}

	// The tag already moved past this build: a late pipeline never drags it back.
	if err := release.RecutAnnotatedTag(root, "v3.7.5", change.String(), "- recut"); err != nil {
		t.Fatal(err)
	}
	if plan, _ := recutPlan(root, builds, "v3.7.5", docs.String()); plan != nil {
		t.Error("an older build than the tagged commit never takes the tag")
	}
	_ = repo
}

func TestMoveTag(t *testing.T) {
	root, _, step := recutRepo(t)
	from := step("release", map[string]string{"Dockerfile": "FROM debian\n"})
	to := step("change", map[string]string{"Dockerfile": "FROM debian\nENV X=0\n"})
	plan := &recut{from: from.String(), to: to.String()}
	tag := func() {
		t.Helper()
		if err := release.RecutAnnotatedTag(root, "v1.0.0", from.String(), "- release"); err != nil {
			t.Fatal(err)
		}
	}
	pushErr := errors.New("push refused")
	refused := errors.New("refused")

	tag()
	fc := &fakeTagForge{}
	if err := moveTag(t.Context(), fc, root, "v1.0.0", plan, func() error { return nil }); err != nil {
		t.Fatalf("clean move: %v", err)
	}
	if got, _ := release.PeelTag(root, "v1.0.0"); got != to.String() || len(fc.deleted) != 1 || len(fc.created) != 0 {
		t.Errorf("the annotated tag moved and was pushed: local=%s forge=%+v", got, fc)
	}

	tag()
	fc = &fakeTagForge{deleteErr: refused}
	if err := moveTag(t.Context(), fc, root, "v1.0.0", plan, func() error { t.Fatal("no push after a refused delete"); return nil }); err == nil {
		t.Error("tag protection that refuses the delete blocks the recut")
	}

	tag()
	fc = &fakeTagForge{}
	if err := moveTag(t.Context(), fc, root, "v1.0.0", plan, func() error { return pushErr }); err != nil {
		t.Errorf("a failed push falls back to the forge's tag: %v", err)
	}
	if len(fc.created) != 1 || fc.created[0] != "v1.0.0@"+to.String() {
		t.Errorf("lightweight tag at the new commit: %v", fc.created)
	}

	tag()
	fc = &fakeTagForge{createErr: map[string]error{to.String(): refused}}
	err := moveTag(t.Context(), fc, root, "v1.0.0", plan, func() error { return pushErr })
	if err == nil || len(fc.created) != 1 || fc.created[0] != "v1.0.0@"+from.String() {
		t.Errorf("the old tag is restored when the new one cannot be placed: err=%v created=%v", err, fc.created)
	}

	tag()
	fc = &fakeTagForge{createErr: map[string]error{to.String(): refused, from.String(): refused}}
	if err := moveTag(t.Context(), fc, root, "v1.0.0", plan, func() error { return pushErr }); err == nil {
		t.Error("a version left without a tag is an error")
	}
}
