package ci

import (
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// stubRemoteHead swaps the network read for a counting stub, restoring it after the test.
func stubRemoteHead(t *testing.T, fn func(branch string) string) *int {
	t.Helper()
	resetFreshnessCache()
	orig := resolveRemoteHead
	calls := 0
	resolveRemoteHead = func(branch string, _ *config.Config) string {
		calls++
		return fn(branch)
	}
	t.Cleanup(func() {
		resolveRemoteHead = orig
		resetFreshnessCache()
	})
	return &calls
}

// The freshness lookup must run at most ONCE per run no matter how many mutating phases
// consult it — the per-phase re-lookup (and its repeated warning) was the noise.
func TestIsBranchHeadFresh_MemoizesLookup(t *testing.T) {
	calls := stubRemoteHead(t, func(string) string { return "abc123" })
	ctx := &CIContext{Provider: "gitlab", Branch: "main", SHA: "abc123"}
	for i := 0; i < 5; i++ {
		if !IsBranchHeadFresh(ctx, nil) {
			t.Fatalf("call %d: expected fresh (remote==local)", i)
		}
	}
	if *calls != 1 {
		t.Errorf("remote lookup ran %d times across 5 calls, want 1 (memoized)", *calls)
	}
}

// A failed lookup fails open (allows execution) AND is memoized — the warning fires once,
// not once per phase.
func TestIsBranchHeadFresh_FailOpenMemoized(t *testing.T) {
	calls := stubRemoteHead(t, func(string) string { return "" }) // unreachable remote
	ctx := &CIContext{Provider: "gitlab", Branch: "main", SHA: "abc123"}
	for i := 0; i < 4; i++ {
		if !IsBranchHeadFresh(ctx, nil) {
			t.Fatalf("call %d: unreachable remote must fail open (true)", i)
		}
	}
	if *calls != 1 {
		t.Errorf("remote lookup ran %d times, want 1 (memoized fail-open)", *calls)
	}
}

// A branch pipeline that lost the HEAD race is NOT fresh.
func TestIsBranchHeadFresh_StaleWhenRemoteMoved(t *testing.T) {
	stubRemoteHead(t, func(string) string { return "newhead" })
	ctx := &CIContext{Provider: "gitlab", Branch: "main", SHA: "oldsha"}
	if IsBranchHeadFresh(ctx, nil) {
		t.Error("remote HEAD moved past this pipeline's SHA — must be stale (false)")
	}
}

// Tag builds, local (non-CI) runs, and missing branch/SHA short-circuit to fresh WITHOUT
// a remote lookup — the memoized network path is never entered.
func TestIsBranchHeadFresh_ShortCircuitsWithoutLookup(t *testing.T) {
	calls := stubRemoteHead(t, func(string) string { return "abc123" })
	cases := []struct {
		name string
		ctx  *CIContext
	}{
		{"local run", &CIContext{}},                                            // not CI
		{"tag build", &CIContext{Provider: "gitlab", Tag: "v1.2.3", SHA: "s"}}, // immutable
		{"no branch", &CIContext{Provider: "gitlab", SHA: "s"}},
		{"no sha", &CIContext{Provider: "gitlab", Branch: "main"}},
	}
	for _, c := range cases {
		if !IsBranchHeadFresh(c.ctx, nil) {
			t.Errorf("%s: expected fresh (short-circuit)", c.name)
		}
	}
	if *calls != 0 {
		t.Errorf("short-circuit cases must not hit the remote; got %d lookups", *calls)
	}
}
