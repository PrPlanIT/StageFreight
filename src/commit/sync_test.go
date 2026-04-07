package commit

import (
	"testing"
)

// ── PlanSync ──────────────────────────────────────────────────────────────────

func TestPlanSync_DetachedHEAD(t *testing.T) {
	state := RepoState{DetachedHEAD: true}
	_, err := PlanSync(state, "origin", "", true)
	if err == nil {
		t.Fatal("expected error for detached HEAD")
	}
}

func TestPlanSync_NoUpstream_SetsUpstreamAndPushes(t *testing.T) {
	state := RepoState{
		Branch:             "main",
		UpstreamConfigured: false,
	}
	plan, err := PlanSync(state, "origin", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActions(t, plan.Steps, []SyncAction{SyncSetUpstream, SyncPush})
}

func TestPlanSync_AheadOnly_JustPushes(t *testing.T) {
	state := RepoState{
		Branch:             "main",
		UpstreamConfigured: true,
		UpstreamRef:        "origin/main",
		AheadCount:         2,
		BehindCount:        0,
	}
	plan, err := PlanSync(state, "origin", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActions(t, plan.Steps, []SyncAction{SyncPush})
}

func TestPlanSync_BehindOnly_FetchFastForwardPush(t *testing.T) {
	state := RepoState{
		Branch:             "main",
		UpstreamConfigured: true,
		UpstreamRef:        "origin/main",
		AheadCount:         0,
		BehindCount:        3,
	}
	plan, err := PlanSync(state, "origin", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActions(t, plan.Steps, []SyncAction{SyncFetch, SyncFastForward, SyncPush})
}

func TestPlanSync_Diverged_RebaseOnDiverge(t *testing.T) {
	state := RepoState{
		Branch:             "main",
		UpstreamConfigured: true,
		UpstreamRef:        "origin/main",
		AheadCount:         1,
		BehindCount:        2,
	}
	plan, err := PlanSync(state, "origin", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActions(t, plan.Steps, []SyncAction{SyncFetch, SyncRebase, SyncPush})
}

func TestPlanSync_Diverged_NoRebase_ReturnsError(t *testing.T) {
	state := RepoState{
		Branch:             "main",
		UpstreamConfigured: true,
		UpstreamRef:        "origin/main",
		AheadCount:         1,
		BehindCount:        2,
	}
	_, err := PlanSync(state, "origin", "", false /* rebaseOnDiverge=false */)
	if err == nil {
		t.Fatal("expected error when diverged and rebaseOnDiverge=false")
	}
}

func TestPlanSync_UpToDate_Noop(t *testing.T) {
	state := RepoState{
		Branch:             "main",
		UpstreamConfigured: true,
		UpstreamRef:        "origin/main",
		AheadCount:         0,
		BehindCount:        0,
	}
	plan, err := PlanSync(state, "origin", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertActions(t, plan.Steps, []SyncAction{SyncNoop})
}

func TestPlanSync_RefspecPassedThrough(t *testing.T) {
	state := RepoState{
		Branch:             "feature/foo",
		UpstreamConfigured: true,
		UpstreamRef:        "origin/main",
		AheadCount:         1,
	}
	plan, err := PlanSync(state, "origin", "HEAD:refs/heads/main", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Refspec != "HEAD:refs/heads/main" {
		t.Errorf("refspec: want %q, got %q", "HEAD:refs/heads/main", plan.Refspec)
	}
}

// ── RepoState helpers ─────────────────────────────────────────────────────────

func TestRepoState_Diverged(t *testing.T) {
	tests := []struct {
		ahead, behind int
		want          bool
	}{
		{1, 1, true},
		{1, 0, false},
		{0, 1, false},
		{0, 0, false},
	}
	for _, tc := range tests {
		s := RepoState{AheadCount: tc.ahead, BehindCount: tc.behind}
		if got := s.Diverged(); got != tc.want {
			t.Errorf("ahead=%d behind=%d: Diverged() want %v, got %v", tc.ahead, tc.behind, tc.want, got)
		}
	}
}

// ── containsAction ────────────────────────────────────────────────────────────

func TestContainsAction(t *testing.T) {
	actions := []SyncAction{SyncFetch, SyncRebase, SyncPush}
	if !containsAction(actions, SyncRebase) {
		t.Error("expected containsAction to find SyncRebase")
	}
	if containsAction(actions, SyncNoop) {
		t.Error("expected containsAction to NOT find SyncNoop")
	}
	if containsAction(nil, SyncPush) {
		t.Error("expected containsAction to return false for nil slice")
	}
}

// ── extractRemote ─────────────────────────────────────────────────────────────

func TestExtractRemote(t *testing.T) {
	tests := []struct {
		upstream string
		want     string
	}{
		{"origin/main", "origin"},
		{"upstream/feature/foo", "upstream"},
		{"main", "origin"}, // no slash → fallback
		{"", "origin"},
	}
	for _, tc := range tests {
		if got := extractRemote(tc.upstream); got != tc.want {
			t.Errorf("extractRemote(%q): want %q, got %q", tc.upstream, tc.want, got)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertActions(t *testing.T, steps []SyncStep, want []SyncAction) {
	t.Helper()
	if len(steps) != len(want) {
		got := make([]SyncAction, len(steps))
		for i, s := range steps {
			got[i] = s.Action
		}
		t.Fatalf("step count: want %v, got %v", want, got)
	}
	for i, step := range steps {
		if step.Action != want[i] {
			t.Errorf("step[%d]: want %q, got %q", i, want[i], step.Action)
		}
	}
}
