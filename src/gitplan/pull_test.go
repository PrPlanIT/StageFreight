package gitplan

import (
	"reflect"
	"testing"
)

func TestResolvePull(t *testing.T) {
	cases := []struct {
		name string
		s    Situation
		want []OpKind
	}{
		{"no upstream → refuse", Situation{Dest: feature, HasUpstream: false}, []OpKind{OpRefuse, OpTeach}},
		{"synced → noop", Situation{Dest: feature, HasUpstream: true}, []OpKind{OpNoop}},
		{"behind → fast-forward", Situation{Dest: feature, HasUpstream: true, Behind: 2}, []OpKind{OpFastForward}},
		{"ahead → nothing to pull", Situation{Dest: feature, HasUpstream: true, Ahead: 2}, []OpKind{OpTeach}},
		{"diverged → Confirm→Replay (rebase local)", Situation{Dest: feature, HasUpstream: true, Ahead: 1, Behind: 2}, []OpKind{OpConfirm, OpReplay}},
		{"in-progress → refuse", Situation{Dest: feature, HasUpstream: true, Behind: 1, InProgressOp: "merge"}, []OpKind{OpRefuse, OpTeach}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kindsOf(ResolvePull(tc.s)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ops = %v, want %v", got, tc.want)
			}
		})
	}
	// pull's diverged rebase is never silent — it is gated by a Confirm before the Replay.
	if got := kindsOf(ResolvePull(Situation{Dest: feature, HasUpstream: true, Ahead: 1, Behind: 1})); got[0] != OpConfirm {
		t.Fatalf("pull diverged must be gated by Confirm before Replay; got %v", got)
	}
}
