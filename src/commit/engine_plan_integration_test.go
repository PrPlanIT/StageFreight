package commit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/gitplan"
	"github.com/PrPlanIT/StageFreight/src/gitstate"
	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func planKinds(p gitplan.Plan) []gitplan.OpKind {
	ks := make([]gitplan.OpKind, len(p.Operations))
	for i, op := range p.Operations {
		ks[i] = op.Kind
	}
	return ks
}

func kindsEq(a []gitplan.OpKind, want ...gitplan.OpKind) bool {
	if len(a) != len(want) {
		return false
	}
	for i := range a {
		if a[i] != want[i] {
			return false
		}
	}
	return true
}

// TestEnginePlan_Integration proves the planner wiring end-to-end on REAL repos:
// a real session's RepoState → Situation → Resolve → the operation graph, and — the
// root-cause fix, live — that the graph is DESTINATION-aware: the same diverged state
// yields Confirm→Replay for a protected destination but Decide for a feature branch.
func TestEnginePlan_Integration(t *testing.T) {
	tmp := t.TempDir()
	remote := filepath.Join(tmp, "remote.git")
	seed := filepath.Join(tmp, "seed")
	local := filepath.Join(tmp, "local")
	other := filepath.Join(tmp, "other")
	url := "file://" + remote
	sig := &object.Signature{Name: "t", Email: "t@t"}

	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	commitFile := func(dir, name, content, msg string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		r, err := git.PlainOpen(dir)
		if err != nil {
			t.Fatal(err)
		}
		wt, _ := r.Worktree()
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: sig}); err != nil {
			t.Fatal(err)
		}
	}

	// Seed the remote with a base commit.
	seedRepo, err := git.PlainInit(seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedRepo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		t.Fatal(err)
	}
	commitFile(seed, "base", "base\n", "base")
	if err := seedRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("seed push: %v", err)
	}

	// local + other clone (upstream tracking configured by clone).
	if _, err := git.PlainClone(local, false, &git.CloneOptions{URL: url}); err != nil {
		t.Fatal(err)
	}
	if _, err := git.PlainClone(other, false, &git.CloneOptions{URL: url}); err != nil {
		t.Fatal(err)
	}

	// --- ahead: one local commit, no divergence → Upload (Automatic) ---
	commitFile(local, "a", "a\n", "local a")
	aheadSess, err := gitstate.OpenSyncSession(local)
	if err != nil {
		t.Fatal(err)
	}
	if got := planKinds(NewEngine(aheadSess, EngineOptions{}).Plan(gitplan.Policy{})); !kindsEq(got, gitplan.OpUpload) {
		t.Fatalf("ahead → want [upload], got %v", got)
	}

	// --- diverge: other pushes a divergent commit; local fetches to see it ---
	commitFile(other, "b", "b\n", "other b")
	otherRepo, _ := git.PlainOpen(other)
	if err := otherRepo.Push(&git.PushOptions{RemoteName: "origin"}); err != nil {
		t.Fatalf("divergent push: %v", err)
	}
	sess, err := gitstate.OpenSyncSession(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Fetch("origin"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	eng := NewEngine(sess, EngineOptions{}) // Plan is read-only; reuse for both policies

	// Same diverged state, two destinations:
	branch := sess.State().Branch // "master" from PlainInit default
	protected := gitplan.Policy{Protected: []string{branch}}
	if got := planKinds(eng.Plan(protected)); !kindsEq(got, gitplan.OpConfirm, gitplan.OpReplay, gitplan.OpUpload) {
		t.Fatalf("diverged + protected → want [confirm replay upload], got %v", got)
	}
	if got := planKinds(eng.Plan(gitplan.Policy{})); !kindsEq(got, gitplan.OpDecide) {
		t.Fatalf("diverged + feature (no policy) → want [decide] (never silent replay), got %v", got)
	}
}
