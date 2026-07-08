package gitplan

import (
	"reflect"
	"testing"
)

func kindsOf(p Plan) []OpKind {
	ks := make([]OpKind, len(p.Operations))
	for i, op := range p.Operations {
		ks[i] = op.Kind
	}
	return ks
}

var (
	feature   = Destination{Remote: "origin", Branch: "feature", Protected: false}
	protected = Destination{Remote: "origin", Branch: "main", Protected: true}
)

// Layer 1 — exhaustive table over (State × Destination × Policy) → exact operation graph
// + derived interaction level.
func TestResolve_Table(t *testing.T) {
	cases := []struct {
		name  string
		s     Situation
		want  []OpKind
		level InteractionLevel
	}{
		{"no upstream, feature", Situation{Dest: feature, HasUpstream: false, Ahead: 3},
			[]OpKind{OpTeach, OpCreateTracking, OpUpload, OpOfferMR}, Inform},
		{"no upstream, protected (no MR offer)", Situation{Dest: protected, HasUpstream: false, Ahead: 3},
			[]OpKind{OpTeach, OpCreateTracking, OpUpload}, Inform},
		{"ahead", Situation{Dest: feature, HasUpstream: true, Ahead: 3},
			[]OpKind{OpUpload}, Automatic},
		{"synced", Situation{Dest: feature, HasUpstream: true},
			[]OpKind{OpNoop}, Automatic},
		{"behind", Situation{Dest: feature, HasUpstream: true, Behind: 2},
			[]OpKind{OpRefuse, OpTeach}, Inform},
		{"diverged feature, no policy → Decide", Situation{Dest: feature, HasUpstream: true, Ahead: 3, Behind: 2},
			[]OpKind{OpDecide}, Decide},
		{"diverged feature, policy=rebase → Confirm→Replay", Situation{Dest: feature, HasUpstream: true, Ahead: 3, Behind: 2, OnDiverge: DivergeRebase},
			[]OpKind{OpConfirm, OpReplay, OpUpload}, Confirm},
		{"diverged protected → Confirm→Replay", Situation{Dest: protected, HasUpstream: true, Ahead: 3, Behind: 2},
			[]OpKind{OpConfirm, OpReplay, OpUpload}, Confirm},
		{"auto-converge behind → fast-forward", Situation{Dest: feature, HasUpstream: true, Behind: 2, AutoConverge: true},
			[]OpKind{OpFastForward}, Automatic},
		{"auto-converge diverged → Confirm→Replay", Situation{Dest: feature, HasUpstream: true, Ahead: 1, Behind: 1, AutoConverge: true},
			[]OpKind{OpConfirm, OpReplay, OpUpload}, Confirm},
		{"in-progress merge → refuse (never acts on half-finished state)", Situation{Dest: feature, HasUpstream: true, Ahead: 3, InProgressOp: "merge"},
			[]OpKind{OpRefuse, OpTeach}, Inform},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Resolve(tc.s)
			if got := kindsOf(p); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ops = %v, want %v", got, tc.want)
			}
			if lvl := p.Interaction(); lvl != tc.level {
				t.Fatalf("interaction = %s, want %s", lvl, tc.level)
			}
		})
	}
}

// The exact afternoon bug, locked forever: a diverged feature branch must NEVER silently
// replay onto trunk — it is a Decide.
func TestResolve_FeatureDivergedNeverSilentlyReplays(t *testing.T) {
	p := Resolve(Situation{Dest: feature, HasUpstream: true, Ahead: 3, Behind: 2})
	for _, op := range p.Operations {
		if op.Kind == OpReplay {
			t.Fatalf("feature-branch diverged must never replay; got %v", kindsOf(p))
		}
	}
	if p.Interaction() != Decide {
		t.Fatalf("feature-branch diverged must be Decide, got %s", p.Interaction())
	}
}

// Layer 2 — structural invariant over ALL emittable plans: any op that mutates shared
// history must be preceded by a Confirm in the same graph. Stops a future cell from ever
// sneaking in a silent rewrite.
func TestResolve_NoUngatedSharedMutation(t *testing.T) {
	for _, prot := range []bool{false, true} {
		for _, rule := range []DivergeRule{DivergeAsk, DivergeRebase} {
			for _, up := range []bool{false, true} {
				for _, ac := range []bool{false, true} {
					for a := 0; a <= 3; a++ {
						for b := 0; b <= 3; b++ {
							s := Situation{
								Dest:         Destination{Remote: "origin", Branch: "x", Protected: prot},
								HasUpstream:  up,
								Ahead:        a,
								Behind:       b,
								OnDiverge:    rule,
								AutoConverge: ac,
							}
							p := Resolve(s)
							confirmed := false
							for _, op := range p.Operations {
								if op.Kind == OpConfirm {
									confirmed = true
								}
								if op.Kind.mutatesSharedHistory() && !confirmed {
									t.Fatalf("ungated shared mutation %s in %v for %+v", op.Kind, kindsOf(p), s)
								}
							}
						}
					}
				}
			}
		}
	}
}

// Layer 2 — determinism: identical inputs yield identical plans (no clock/random).
func TestResolve_Deterministic(t *testing.T) {
	s := Situation{Dest: protected, HasUpstream: true, Ahead: 2, Behind: 1}
	if !reflect.DeepEqual(Resolve(s), Resolve(s)) {
		t.Fatal("Resolve is not deterministic for identical input")
	}
}
