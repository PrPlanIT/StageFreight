// Package forgesim proves StageFreight's git workflow against a real forge: it drives a
// stagefreight binary — a release, latest-dev, or a branch's test image — through commit,
// push, tag and worktree on a throwaway repository, and puts the repository back the way it
// found it.
//
// It runs only when SF_SIM_TOKEN is set, through a test suite whose env: names the SF_SIM_*
// variables (tests run hermetically and see only what a suite names):
//
//	SF_SIM_TOKEN=… stagefreight test --pkg ./internal/forgesim/ --run TestForgeSim
//
//	SF_SIM_TOKEN    token for the throwaway project (its access level decides the role ceiling)
//	SF_SIM_URL      forge base URL (default https://git.optcp.com)
//	SF_SIM_PROJECT  project path (default HomeLabHD/Training-Dummy)
//	SF_SIM_BINARY   the stagefreight binary to drive (default: stagefreight on PATH)
//
// The repository's own branches are never touched: the sim works on a main it creates
// (refusing to start when a main it didn't create exists), feature/sim-* branches and
// sim-* tags, and deletes all of them when it finishes, pass or fail.
package forgesim

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// seedMarker identifies the main branch the sim created, so a later run may reclaim it and
// never a main it didn't create.
const seedMarker = "stagefreight-sim seed"

// seedConfig is the throwaway repository's committed .stagefreight.yml. It holds only what
// every build understands, so one seed serves a build without the workflow gate too; the
// workflow scenario adds workflowConfig to its own working copy.
const seedConfig = `version: 1

forges:
  forge:
    provider: gitlab
    url: "{URL}"
    credentials: SIM_FORGE

repos:
  primary:
    forge: forge
    project: "{PROJECT}"
    roles: [primary]
    branches:
      default: main

git:
  branches:
    main: "^main$"
    feature: "^feature/"
    stable: "^stable$"
`

// workflowConfig is the trunk workflow, plus a long-lived stable branch that takes direct
// pushes (a director's to move).
const workflowConfig = `
workflow:
  branches:
    main:    { match: main, landing: merge_request }
    feature: { match: feature, lifetime: short, max_age: 14d }
    stable:  { match: stable, landing: direct }
  promote:
    land: { from: feature, to: main, method: merge_request }
  release:
    from:
      main: [prerelease, major, minor, patch]
`

type sim struct {
	t       *testing.T
	url     string // repository URL
	auth    transport.AuthMethod
	token   string
	binary  string
	work    string   // scratch root
	created []string // remote refs this run created, deleted at the end
}

func newSim(t *testing.T) *sim {
	t.Helper()
	token := os.Getenv("SF_SIM_TOKEN")
	if token == "" {
		t.Skip("SF_SIM_TOKEN not set: the forge sim runs only against a throwaway project")
	}
	base := envOr("SF_SIM_URL", "https://git.optcp.com")
	project := envOr("SF_SIM_PROJECT", "HomeLabHD/Training-Dummy")
	binary := envOr("SF_SIM_BINARY", "stagefreight")
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatalf("binary %q: %v", binary, err)
	}
	s := &sim{
		t:      t,
		url:    strings.TrimRight(base, "/") + "/" + project + ".git",
		auth:   &githttp.BasicAuth{Username: "oauth2", Password: token},
		token:  token,
		binary: binary,
		work:   t.TempDir(),
	}
	t.Cleanup(s.teardown)
	s.seed(strings.TrimRight(base, "/"), project)
	return s
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// remoteRefs lists the remote's refs by name.
func (s *sim) remoteRefs() map[plumbing.ReferenceName]plumbing.Hash {
	s.t.Helper()
	r := git.NewRemote(nil, &gitconfig.RemoteConfig{Name: "origin", URLs: []string{s.url}})
	refs, err := r.List(&git.ListOptions{Auth: s.auth})
	if err != nil && !errors.Is(err, transport.ErrEmptyRemoteRepository) {
		s.t.Fatalf("listing %s: %v", s.url, err)
	}
	out := map[plumbing.ReferenceName]plumbing.Hash{}
	for _, ref := range refs {
		out[ref.Name()] = ref.Hash()
	}
	return out
}

// seed creates main as an orphan commit carrying only the sim's config. A main the sim
// didn't create stops the run before anything is written.
func (s *sim) seed(base, project string) {
	s.t.Helper()
	dir := filepath.Join(s.work, "seed")
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		s.t.Fatal(err)
	}
	cfg := strings.NewReplacer("{URL}", base, "{PROJECT}", project).Replace(seedConfig)
	write(s.t, filepath.Join(dir, ".stagefreight.yml"), cfg)
	write(s.t, filepath.Join(dir, "README.md"), "Scratch state for StageFreight's forge sim. Deleted when the run ends.\n")
	wt, _ := repo.Worktree()
	if err := wt.AddGlob("."); err != nil {
		s.t.Fatal(err)
	}
	h, err := wt.Commit(seedMarker, &git.CommitOptions{Author: &object.Signature{Name: "stagefreight-sim", Email: "sim@stagefreight.invalid", When: time.Now()}})
	if err != nil {
		s.t.Fatal(err)
	}

	if existing, ok := s.remoteRefs()["refs/heads/main"]; ok {
		if !s.isSeed(existing) {
			s.t.Fatalf("%s already has a main the sim didn't create; refusing to touch it", s.url)
		}
	}
	if _, err := repo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{s.url}}); err != nil {
		s.t.Fatal(err)
	}
	s.push(repo, "+"+h.String()+":refs/heads/main")
	s.created = append(s.created, "refs/heads/main")
}

// isSeed reports whether a remote commit is a sim seed, by fetching it.
func (s *sim) isSeed(h plumbing.Hash) bool {
	dir := filepath.Join(s.work, "probe")
	repo, err := git.PlainClone(dir, true, &git.CloneOptions{URL: s.url, Auth: s.auth, ReferenceName: "refs/heads/main", SingleBranch: true, Depth: 1})
	if err != nil {
		return false
	}
	c, err := repo.CommitObject(h)
	return err == nil && strings.HasPrefix(c.Message, seedMarker)
}

func (s *sim) push(repo *git.Repository, spec string) {
	s.t.Helper()
	err := repo.Push(&git.PushOptions{RemoteName: "origin", Auth: s.auth, RefSpecs: []gitconfig.RefSpec{gitconfig.RefSpec(spec)}})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		s.t.Fatalf("push %s: %v", spec, err)
	}
}

// teardown deletes every ref the sim may have made: what it recorded, plus any
// feature/sim-*, stable and sim-* tag on the remote.
func (s *sim) teardown() {
	refs := s.remoteRefs()
	del := map[plumbing.ReferenceName]bool{}
	for _, r := range s.created {
		del[plumbing.ReferenceName(r)] = true
	}
	for name := range refs {
		n := name.String()
		if strings.HasPrefix(n, "refs/heads/feature/sim-") || n == "refs/heads/stable" || strings.HasPrefix(n, "refs/tags/sim-") {
			del[name] = true
		}
	}
	var specs []gitconfig.RefSpec
	for name := range del {
		if _, ok := refs[name]; ok {
			specs = append(specs, gitconfig.RefSpec(":"+name.String()))
		}
	}
	if len(specs) == 0 {
		return
	}
	r := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{s.url}})
	if err := r.Push(&git.PushOptions{Auth: s.auth, RefSpecs: specs}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		s.t.Errorf("teardown: deleting %v: %v — remove them by hand", specs, err)
	}
}

// clone makes a working clone of main for a scenario.
func (s *sim) clone(name string) string {
	s.t.Helper()
	dir := filepath.Join(s.work, name)
	if _, err := git.PlainClone(dir, false, &git.CloneOptions{URL: s.url, Auth: s.auth, ReferenceName: "refs/heads/main"}); err != nil {
		s.t.Fatalf("clone: %v", err)
	}
	repo, _ := git.PlainOpen(dir)
	cfg, _ := repo.Config()
	cfg.User.Name, cfg.User.Email = "stagefreight-sim", "sim@stagefreight.invalid"
	if err := repo.SetConfig(cfg); err != nil {
		s.t.Fatal(err)
	}
	return dir
}

// run executes the binary in dir as a person at the keyboard would: no CI context, and
// only the sim's forge credential.
func (s *sim) run(dir string, args ...string) (string, error) {
	cmd := exec.Command(s.binary, args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "CI" || strings.HasPrefix(k, "SF_CI_") || strings.HasPrefix(k, "CI_") || strings.HasSuffix(k, "_TOKEN") {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, "SIM_FORGE_TOKEN="+s.token, "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// refuses runs a command that must fail, with every want in its output.
func (s *sim) refuses(dir string, wants []string, args ...string) {
	s.t.Helper()
	out, err := s.run(dir, args...)
	if err == nil {
		s.t.Errorf("stagefreight %s: want refused, it succeeded:\n%s", strings.Join(args, " "), out)
		return
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			s.t.Errorf("stagefreight %s: output lacks %q:\n%s", strings.Join(args, " "), w, out)
		}
	}
}

// succeeds runs a command that must succeed, with every want in its output.
func (s *sim) succeeds(dir string, wants []string, args ...string) string {
	s.t.Helper()
	out, err := s.run(dir, args...)
	if err != nil {
		s.t.Errorf("stagefreight %s: %v\n%s", strings.Join(args, " "), err, out)
		return out
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			s.t.Errorf("stagefreight %s: output lacks %q:\n%s", strings.Join(args, " "), w, out)
		}
	}
	return out
}

// supports reports whether the binary's help for a command mentions what a feature adds,
// so one sim drives any build.
func (s *sim) supports(command, mention string) bool {
	out, err := s.run(s.work, command, "--help")
	return err == nil && strings.Contains(out, mention)
}

func (s *sim) head(dir string) plumbing.Hash {
	s.t.Helper()
	repo, err := git.PlainOpen(dir)
	if err != nil {
		s.t.Fatal(err)
	}
	h, err := repo.Head()
	if err != nil {
		s.t.Fatal(err)
	}
	return h.Hash()
}

// checkout switches dir to branch, creating it when create is set, keeping uncommitted
// changes (the workflow scenario's config) as git switch does.
func (s *sim) checkout(dir, branch string, create bool) {
	s.t.Helper()
	repo, _ := git.PlainOpen(dir)
	wt, _ := repo.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Create: create, Keep: true}); err != nil {
		s.t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestForgeSim drives the binary through the declared workflow on the real forge.
func TestForgeSim(t *testing.T) {
	s := newSim(t)
	t.Run("workflow", func(t *testing.T) {
		if !s.supports("commit", "--role") {
			t.Skip("this build has no workflow gate (commit --role)")
		}
		s.t = t
		simWorkflow(s)
	})
	t.Run("worktree", func(t *testing.T) {
		if !s.supports("worktree", "remove") {
			t.Skip("this build has no worktree command")
		}
		s.t = t
		simWorktree(s)
	})
}

func simWorkflow(s *sim) {
	dir := s.clone("workflow")
	cfgPath := filepath.Join(dir, ".stagefreight.yml")
	seed, err := os.ReadFile(cfgPath)
	if err != nil {
		s.t.Fatal(err)
	}
	write(s.t, cfgPath, string(seed)+workflowConfig)

	// Your access on the forge bounds --role: the sim's token is read live.
	write(s.t, filepath.Join(dir, "a.txt"), "a\n")
	before := s.head(dir)
	s.refuses(dir, []string{"main is a main branch", "through a merge request", "feature branch (matching ^feature/)"},
		"commit", "-t", "docs", "-m", "direct to main", "--", "a.txt")
	if s.head(dir) != before {
		s.t.Error("a refused commit must not be made")
	}

	// Asking for more than your access grants is refused, never quietly lowered.
	if out, err := s.run(dir, "commit", "--role", "producer", "-t", "docs", "-m", "x", "--", "a.txt"); err == nil || !strings.Contains(out, "can only lower") {
		s.t.Logf("--role producer was accepted; the token's access must grant producer (an owner). output:\n%s", out)
	}

	// A producer's override is the only way past, and a director isn't one.
	s.refuses(dir, []string{"needs the producer role"},
		"commit", "--role", "director", "--maintainer-override", "-t", "docs", "-m", "x", "--", "a.txt")

	// Work lands on a feature branch, which pushes freely.
	s.checkout(dir, "feature/sim-work", true)
	s.succeeds(dir, nil, "commit", "-t", "docs", "-m", "work on a feature branch", "--", "a.txt")
	s.succeeds(dir, nil, "push", "--refspec", "HEAD:refs/heads/feature/sim-work")
	if _, ok := s.remoteRefs()["refs/heads/feature/sim-work"]; !ok {
		s.t.Error("feature branch didn't reach the forge")
	}

	// Pushing it straight onto main is refused; main is unchanged.
	mainBefore := s.remoteRefs()["refs/heads/main"]
	s.refuses(dir, []string{"through a merge request"}, "push", "--refspec", "HEAD:main")
	if s.remoteRefs()["refs/heads/main"] != mainBefore {
		s.t.Error("a refused push moved main")
	}

	// A long-lived branch that takes direct pushes is a director's to move.
	s.refuses(dir, []string{"needs the director role", "--role director"}, "push", "--refspec", "HEAD:refs/heads/stable")
	s.succeeds(dir, nil, "push", "--role", "director", "--refspec", "HEAD:refs/heads/stable")
	if _, ok := s.remoteRefs()["refs/heads/stable"]; !ok {
		s.t.Error("director's push to stable didn't land")
	}

	// Cutting a release is a producer's call, from a class allowed to carry it.
	s.refuses(dir, []string{"cutting a release needs the producer role"}, "tag", "v0.1.0", "-m", "- sim", "-y", "--dry-run")
	s.refuses(dir, []string{"cutting a release needs the producer role"}, "tag", "v0.1.0", "-m", "- sim", "-y", "--dry-run", "--role", "director")

	// evaluate reports and lets the action through.
	dir = s.clone("evaluate")
	cfgPath = filepath.Join(dir, ".stagefreight.yml")
	write(s.t, cfgPath, string(seed)+strings.Replace(workflowConfig,
		"main:    { match: main, landing: merge_request }",
		"main:    { match: main, landing: merge_request, enforcement: evaluate }", 1))
	write(s.t, filepath.Join(dir, "b.txt"), "b\n")
	s.succeeds(dir, []string{"evaluate, not enforced"}, "commit", "-t", "docs", "-m", "evaluated", "--", "b.txt")
}

func simWorktree(s *sim) {
	dir := s.clone("worktree")
	side := filepath.Join(s.work, "side")

	s.succeeds(dir, []string{"feature/sim-side (new)", "created"}, "worktree", "add", "-b", "feature/sim-side", side)
	s.succeeds(dir, []string{side, "feature/sim-side"}, "worktree", "list")

	// The branch is checked out there, so a second checkout of it is refused, as git does.
	s.refuses(dir, []string{"already checked out"}, "worktree", "add", filepath.Join(s.work, "again"), "feature/sim-side")

	// A commit that exists nowhere else holds the worktree until it's pushed or forced.
	write(s.t, filepath.Join(side, "c.txt"), "c\n")
	s.succeeds(side, nil, "commit", "-t", "docs", "-m", "only in the side worktree", "--", "c.txt")
	s.refuses(dir, []string{"no other branch has", "--force"}, "worktree", "remove", side)

	s.succeeds(side, nil, "push", "--refspec", "HEAD:refs/heads/feature/sim-side")
	s.succeeds(dir, []string{"removed", "feature/sim-side kept"}, "worktree", "remove", "--force", side)
	if _, err := os.Stat(side); !errors.Is(err, os.ErrNotExist) {
		s.t.Error("worktree directory remains")
	}
}
