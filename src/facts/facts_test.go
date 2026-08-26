package facts

import (
	"strings"
	"testing"
)

// recordingResolver is a test double: it records the order in which resolvers run and
// appends its name to the text, so both ordering and application are observable.
type recordingResolver struct {
	name     string
	provides []string
	deps     []string
	log      *[]string
}

func (r recordingResolver) Name() string       { return r.name }
func (r recordingResolver) Provides() []string  { return r.provides }
func (r recordingResolver) DependsOn() []string { return r.deps }
func (r recordingResolver) Resolve(values []string, _ *Context) []string {
	*r.log = append(*r.log, r.name)
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = v + r.name
	}
	return out
}

// TestRegistry_TopologicalOrder verifies resolvers run after everything they depend
// on, regardless of registration order, and that Resolve applies them in that order.
func TestRegistry_TopologicalOrder(t *testing.T) {
	var log []string
	// c depends on b, b depends on a. Register in REVERSE (c, b, a) to prove the
	// registry reorders by dependency rather than trusting registration order.
	reg := New().
		Add(recordingResolver{name: "c", provides: []string{"c"}, deps: []string{"b"}, log: &log}).
		Add(recordingResolver{name: "b", provides: []string{"b"}, deps: []string{"a"}, log: &log}).
		Add(recordingResolver{name: "a", provides: []string{"a"}, log: &log})

	got := reg.ResolveOne("", nil)

	if want := "abc"; got != want {
		t.Errorf("applied text = %q, want %q (dependency order a→b→c)", got, want)
	}
	if want := "a,b,c"; strings.Join(log, ",") != want {
		t.Errorf("run order = %q, want %q", strings.Join(log, ","), want)
	}
}

// TestRegistry_UnknownDependencyIsExternal verifies a DependsOn family that no
// resolver provides imposes no constraint (it's treated as externally supplied) and
// does not drop the resolver.
func TestRegistry_UnknownDependencyIsExternal(t *testing.T) {
	var log []string
	reg := New().
		Add(recordingResolver{name: "x", provides: []string{"x"}, deps: []string{"nowhere"}, log: &log})
	_ = reg.ResolveOne("", nil)
	if len(log) != 1 || log[0] != "x" {
		t.Errorf("run log = %v, want [x] (unknown dep ignored, resolver still runs)", log)
	}
}

// TestRegistry_IndependentKeepRegistrationOrder verifies resolvers with no dependency
// between them run in registration order (the deterministic tiebreaker).
func TestRegistry_IndependentKeepRegistrationOrder(t *testing.T) {
	var log []string
	reg := New().
		Add(recordingResolver{name: "1", provides: []string{"1"}, log: &log}).
		Add(recordingResolver{name: "2", provides: []string{"2"}, log: &log})
	_ = reg.ResolveOne("", nil)
	if want := "1,2"; strings.Join(log, ",") != want {
		t.Errorf("run order = %q, want %q", strings.Join(log, ","), want)
	}
}
