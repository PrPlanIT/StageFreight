package retention

import (
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/config"
)

func itemsNewestFirst(names []string, spacing time.Duration) []Item {
	now := time.Now()
	out := make([]Item, len(names))
	for i, n := range names {
		out[i] = Item{Name: n, CreatedAt: now.Add(-time.Duration(i) * spacing)}
	}
	return out
}

// The scoped grammar: refs partition candidates by first-matching pattern; each
// partition is evaluated under its own effective policy (unset fields inherit the
// default). One engine point — every RetentionPolicy consumer inherits this.
func TestApplyPoliciesScoped_RefsOverridePerScope(t *testing.T) {
	// 4 go versions and 3 trivy versions interleaved, newest-first.
	cands := itemsNewestFirst([]string{
		"go/1.26.7", "trivy/0.72.0", "go/1.26.6", "trivy/0.69.3",
		"go/1.26.5", "trivy/0.65.0", "go/1.26.4",
	}, time.Hour)

	policy := config.RetentionPolicy{
		KeepLast: 2, // default
		Refs: []config.RetentionRef{
			{Match: "go/{v}", KeepLast: 3},    // keep more Go lines
			{Match: "trivy/{v}", KeepLast: 1}, // scanners: newest only
		},
	}

	keep := ApplyPoliciesScoped(cands, policy)
	got := map[string]bool{}
	for i, c := range cands {
		got[c.Name] = keep[i]
	}

	for name, want := range map[string]bool{
		"go/1.26.7":    true, // go scope keeps 3
		"go/1.26.6":    true,
		"go/1.26.5":    true,
		"go/1.26.4":    false,
		"trivy/0.72.0": true, // trivy scope keeps 1
		"trivy/0.69.3": false,
		"trivy/0.65.0": false,
	} {
		if got[name] != want {
			t.Errorf("%s: keep=%v, want %v", name, got[name], want)
		}
	}
}

// An item matching no ref falls through to the DEFAULT policy, counted within its own
// (default) partition — refs never leak their budgets across scopes.
func TestApplyPoliciesScoped_DefaultPartition(t *testing.T) {
	cands := itemsNewestFirst([]string{"syft/1.46.0", "syft/1.42.3", "syft/1.40.0"}, time.Hour)
	policy := config.RetentionPolicy{
		KeepLast: 2,
		Refs:     []config.RetentionRef{{Match: "go/{v}", KeepLast: 3}},
	}
	keep := ApplyPoliciesScoped(cands, policy)
	if !keep[0] || !keep[1] || keep[2] {
		t.Errorf("default partition must apply default keep_last=2, got %v", keep)
	}
}

// max_age is an additive keep rule in the SAME grammar: alone it keeps only the recent;
// combined with keep_last it can only keep MORE.
func TestApplyPolicies_MaxAge(t *testing.T) {
	now := time.Now()
	cands := []Item{
		{Name: "fresh", CreatedAt: now.Add(-2 * time.Hour)},
		{Name: "week", CreatedAt: now.Add(-7 * 24 * time.Hour)},
		{Name: "month", CreatedAt: now.Add(-31 * 24 * time.Hour)},
	}
	keep := ApplyPolicies(cands, config.RetentionPolicy{MaxAge: "3d"})
	if !keep[0] || keep[1] || keep[2] {
		t.Errorf("max_age alone must keep only the recent, got %v", keep)
	}
	keep = ApplyPolicies(cands, config.RetentionPolicy{KeepLast: 2, MaxAge: "3d"})
	if !keep[0] || !keep[1] || keep[2] {
		t.Errorf("keep_last+max_age are additive, got %v", keep)
	}
}

// Effective inherits unset ref fields from the default, and policy-global fields
// (Protect) always carry over.
func TestEffective_Inheritance(t *testing.T) {
	policy := config.RetentionPolicy{
		KeepLast: 2, KeepDaily: 7, Protect: []string{"pinned*"},
		Refs: []config.RetentionRef{{Match: "go/{v}", KeepLast: 4}},
	}
	eff := Effective(policy, "go/1.26.7")
	if eff.KeepLast != 4 || eff.KeepDaily != 7 || len(eff.Protect) != 1 || eff.Refs != nil {
		t.Errorf("ref override must inherit unset fields + globals, drop refs; got %+v", eff)
	}
	def := Effective(policy, "trivy/0.72.0")
	if def.KeepLast != 2 || def.Refs != nil {
		t.Errorf("non-matching name must resolve to the default, got %+v", def)
	}
}
