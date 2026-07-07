package docker

import "testing"

func TestCrucibleStepOutput(t *testing.T) {
	cases := []struct {
		name               string
		local, transport   bool
		wantLoad, wantPush bool
	}{
		{"local loads, never pushes", true, false, true, false},
		{"local wins over transport", true, true, true, false},
		{"transport neither loads nor pushes", false, true, false, false},
		{"default pushes to registry", false, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			load, push := crucibleStepOutput(tc.local, tc.transport)
			if load != tc.wantLoad || push != tc.wantPush {
				t.Fatalf("crucibleStepOutput(local=%v, transport=%v) = (load=%v, push=%v), want (load=%v, push=%v)",
					tc.local, tc.transport, load, push, tc.wantLoad, tc.wantPush)
			}
		})
	}

	// The property this locks forever: --local NEVER pushes, whatever transport is. Its absence
	// was the bug — a local build fell through to push and died on "tag is needed when pushing
	// to registry", leaving no image in the daemon.
	t.Run("local never pushes", func(t *testing.T) {
		for _, transport := range []bool{false, true} {
			if _, push := crucibleStepOutput(true, transport); push {
				t.Fatalf("--local must never push (transport=%v)", transport)
			}
		}
	})
}
