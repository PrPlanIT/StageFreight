package upstream

import (
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/upstream/merge"
)

// nutWorld: upstream v1 → v2 edits README.adoc, docs/manual.txt, src/main.c and its own
// .gitlab-ci.yml; our fork (from v1) deleted README.adoc, patched src/main.c elsewhere, and
// renders its own .gitlab-ci.yml.
func nutWorld(t *testing.T) (repo *git.Repository, v1, v2, ours plumbing.Hash) {
	repo, _ = git.Init(memory.NewStorage(), nil)
	main := "int main() {\n  setup();\n  run();\n  return 0;\n}\n"
	v1files := map[string]string{
		"README.adoc": "= NUT\nold intro\n", "docs/manual.txt": "manual v1\n",
		"src/main.c": main, ".gitlab-ci.yml": "upstream ci v1\n",
	}
	v1 = mk(t, repo, v1files, "v1")
	v2 = mk(t, repo, map[string]string{
		"README.adoc": "= NUT\nnew intro\n", "docs/manual.txt": "manual v2\n",
		"src/main.c": "int main() {\n  setup();\n  run();\n  return 0; /* v2 */\n}\n", ".gitlab-ci.yml": "upstream ci v2\n",
	}, "v2", v1)
	ours = mk(t, repo, map[string]string{
		"docs/manual.txt": "manual v1\n",
		"src/main.c":      "int main() {\n  setup_ours();\n  run();\n  return 0;\n}\n",
		".gitlab-ci.yml":  "rendered by stagefreight\n",
	}, "our patches: drop README.adoc, our setup, our CI", v1)
	return
}

func opts(strategy string, rules ...config.UpstreamFileRule) SyncOptions {
	return SyncOptions{Strategy: strategy, Policy: FilePolicy(rules), Sig: sig, MergeMessage: "merge", HoldMessage: "hold"}
}

func TestSyncFiles_DeletedReadmeConflictsUnlessForkHoldsIt(t *testing.T) {
	repo, _, v2, ours := nutWorld(t)
	for _, s := range []string{config.SyncRebase, config.SyncMerge} {
		plan, _ := PlanSync(repo, ours, v2, opts(s))
		if plan.Result != SyncConflict || strings.Join(plan.Conflicts, ",") != "README.adoc" {
			t.Fatalf("%s without a rule: deleted-vs-edited README is a conflict: %+v", s, plan)
		}
		plan, err := PlanSync(repo, ours, v2, opts(s, config.UpstreamFileRule{Match: "README.adoc", Take: "fork"}))
		if err != nil || plan.Result != SyncSynced {
			t.Fatalf("%s: %+v %v", s, plan, err)
		}
		if file(t, repo, plan.NewHead, "README.adoc") != "<missing>" {
			t.Errorf("%s: our deletion stands", s)
		}
		if !strings.Contains(strings.Join(plan.Held, ","), "README.adoc") {
			t.Errorf("%s: held = %v, want README.adoc reported", s, plan.Held)
		}
		if got := file(t, repo, plan.NewHead, "src/main.c"); !strings.Contains(got, "setup_ours") || !strings.Contains(got, "/* v2 */") {
			t.Errorf("%s: non-overlapping edits still merge line by line: %q", s, got)
		}
		if file(t, repo, plan.NewHead, ".gitlab-ci.yml") != "rendered by stagefreight\n" {
			t.Errorf("%s: StageFreight's rendered CI always keeps the fork's version", s)
		}
		if file(t, repo, plan.NewHead, "docs/manual.txt") != "manual v2\n" {
			t.Errorf("%s: files without a rule take upstream's change", s)
		}
	}
}

func TestSyncFiles_RebaseHoldsUntouchedFilesInTheirOwnCommit(t *testing.T) {
	repo, _, v2, ours := nutWorld(t)
	plan, err := PlanSync(repo, ours, v2, opts(config.SyncRebase,
		config.UpstreamFileRule{Match: "README.adoc", Take: "fork"},
		config.UpstreamFileRule{Match: "docs/**", Take: "fork"}))
	if err != nil || plan.Result != SyncSynced {
		t.Fatalf("%+v %v", plan, err)
	}
	head, _ := repo.CommitObject(plan.NewHead)
	if !strings.HasPrefix(head.Message, "hold") || !strings.Contains(head.Message, "Held: docs/manual.txt") {
		t.Fatalf("the hold is its own commit on top: %q", head.Message)
	}
	parent, _ := head.Parent(0)
	if !strings.HasPrefix(parent.Message, "our patches") || file(t, repo, parent.Hash, "docs/manual.txt") != "manual v2\n" {
		t.Fatalf("the replayed commit carries only its own changes: %q", parent.Message)
	}
	if file(t, repo, plan.NewHead, "docs/manual.txt") != "manual v1\n" {
		t.Fatal("docs keep the fork's version")
	}
}

func TestSyncFiles_UpstreamOverridesAndReviewGates(t *testing.T) {
	repo, _, v2, ours := nutWorld(t)
	plan, _ := PlanSync(repo, ours, v2, opts(config.SyncMerge,
		config.UpstreamFileRule{Match: "README.adoc", Take: "fork"},
		config.UpstreamFileRule{Match: "src/*.c", Take: "upstream"}))
	if file(t, repo, plan.NewHead, "src/main.c") != "int main() {\n  setup();\n  run();\n  return 0; /* v2 */\n}\n" || strings.Join(plan.Overridden, ",") != "src/main.c" {
		t.Fatalf("take: upstream replaces our edit and says so: %+v", plan)
	}
	plan, _ = PlanSync(repo, ours, v2, opts(config.SyncRebase,
		config.UpstreamFileRule{Match: "README.adoc", Take: "fork"},
		config.UpstreamFileRule{Match: "docs/**", Take: "review"}))
	if plan.Result != SyncConflict || strings.Join(plan.Conflicts, ",") != "docs/manual.txt" {
		t.Fatalf("review routes even a clean upstream change to a human: %+v", plan)
	}
}

func TestSyncFiles_FastForwardStillHolds(t *testing.T) {
	repo, v1, v2, _ := nutWorld(t)
	plan, err := PlanSync(repo, v1, v2, opts(config.SyncRebase, config.UpstreamFileRule{Match: "docs/**", Take: "fork"}))
	if err != nil || plan.Result != SyncSynced || plan.Rewrites || plan.NewHead == v2 {
		t.Fatalf("nothing of ours on top, but a hold needs a commit on upstream's: %+v %v", plan, err)
	}
	head, _ := repo.CommitObject(plan.NewHead)
	if head.ParentHashes[0] != v2 || file(t, repo, plan.NewHead, "docs/manual.txt") != "manual v1\n" {
		t.Fatal("upstream's v2 plus the held file")
	}
}

func TestFilePolicy_LastMatchWins(t *testing.T) {
	p := FilePolicy([]config.UpstreamFileRule{
		{Match: "**", Take: "fork"},
		{Match: "src/**", Take: "merge"},
		{Match: "src/vendor/**", Take: "upstream"},
	})
	for path, want := range map[string]string{
		"README.md": merge.Fork, "src/a.c": merge.Merge, "src/vendor/x.c": merge.Upstream,
		".stagefreight.yml": merge.Fork, ".github/workflows/stagefreight.yml": merge.Fork, ".stagefreight/scribe/a.svg": merge.Fork,
	} {
		if got := p(path); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
	if FilePolicy([]config.UpstreamFileRule{{Match: ".gitlab-ci.yml", Take: "upstream"}})(".gitlab-ci.yml") != merge.Fork {
		t.Error("StageFreight's own files cannot be handed to upstream")
	}
}

// TestSyncFiles_BinaryBlobPastSniffIsNotMerged guards the whole-blob scan: a file that is text
// for the first 8000 bytes (git's binary-sniff window) but has a NUL after must be treated as
// binary and refused, not line-merged into a corrupted blob — even though the edits below do
// not overlap and would otherwise merge cleanly.
func TestSyncFiles_BinaryBlobPastSniffIsNotMerged(t *testing.T) {
	repo, _ := git.Init(memory.NewStorage(), nil)
	var head strings.Builder
	for head.Len() <= 8000 {
		head.WriteString("filler line\n")
	}
	h := head.String()
	base := mk(t, repo, map[string]string{"asset": h + "\x00top\nmid\nbottom\n"}, "base")
	up := mk(t, repo, map[string]string{"asset": h + "\x00top\nmid\nBOTTOM\n"}, "up", base)
	ours := mk(t, repo, map[string]string{"asset": h + "\x00TOP\nmid\nbottom\n"}, "ours", base)
	plan, err := PlanSync(repo, ours, up, opts(config.SyncMerge))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Result != SyncConflict {
		t.Fatalf("a binary file changed on both sides must conflict, not merge: %+v", plan)
	}
}
