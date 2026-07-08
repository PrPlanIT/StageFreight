package gitplan

import (
	"reflect"
	"testing"
)

func TestResolveExplicitPush(t *testing.T) {
	feat := Destination{Remote: "origin", Branch: "feature", Protected: false}
	main := Destination{Remote: "origin", Branch: "main", Protected: true}
	cases := []struct {
		name string
		s    Situation
		want []OpKind
	}{
		{"target missing → create", Situation{Dest: feat, HasUpstream: false, Ahead: 2}, []OpKind{OpTeach, OpDirectPush}},
		{"synced → noop", Situation{Dest: feat, HasUpstream: true}, []OpKind{OpNoop}},
		{"destination ahead → refuse", Situation{Dest: feat, HasUpstream: true, Behind: 2}, []OpKind{OpRefuse, OpTeach}},
		{"ahead of unprotected → direct push (ff)", Situation{Dest: feat, HasUpstream: true, Ahead: 3}, []OpKind{OpDirectPush}},
		{"ahead of protected trunk → Confirm→push", Situation{Dest: main, HasUpstream: true, Ahead: 3}, []OpKind{OpConfirm, OpDirectPush}},
		{"diverged → refuse + rebase-onto guidance", Situation{Dest: main, HasUpstream: true, Ahead: 2, Behind: 3}, []OpKind{OpRefuse, OpTeach}},
		{"in-progress → refuse", Situation{Dest: main, HasUpstream: true, Ahead: 1, InProgressOp: "merge"}, []OpKind{OpRefuse, OpTeach}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kindsOf(ResolveExplicitPush(tc.s)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ops = %v, want %v", got, tc.want)
			}
		})
	}
	// The push must target the DESTINATION branch, not the current upstream.
	p := ResolveExplicitPush(Situation{Dest: Destination{Remote: "origin", Branch: "main"}, HasUpstream: true, Ahead: 1})
	if p.Operations[0].Detail != "HEAD:refs/heads/main" {
		t.Fatalf("expected refspec HEAD:refs/heads/main, got %q", p.Operations[0].Detail)
	}
}
