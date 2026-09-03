package presetref

import (
	"errors"
	"testing"
)

// The three positions on a moved pin, each with a reason someone holds it: stop and
// investigate, trust the source, or keep what we had. None is universally right, which
// is why the resolver must not choose.
func TestMismatchPolicy(t *testing.T) {
	ref := Parse("src//p.yml@refs/tags/v1")
	newResolver := func(p MismatchPolicy) (Resolver, mapCache, *Outcome) {
		var got Outcome
		c := mapCache{CacheKey(ref): []byte("retained")}
		return Resolver{
			Fetcher:    &revFetcher{body: []byte("moved")},
			Cache:      c,
			OnMismatch: p,
			Observe:    func(o Outcome) { got = o },
		}, c, &got
	}

	t.Run("fail stops and hands back both sides", func(t *testing.T) {
		r, cache, got := newResolver(MismatchFail)
		body, err := r.Resolve(ref)
		var v *PinViolation
		if !errors.As(err, &v) {
			t.Fatalf("err = %v, want PinViolation", err)
		}
		if body != nil {
			t.Error("a stop must not resolve to either side")
		}
		if string(cache[CacheKey(ref)]) != "retained" {
			t.Error("failing must not overwrite the evidence")
		}
		if !got.Violated {
			t.Error("outcome must record the violation")
		}
	})

	t.Run("source takes what the source serves", func(t *testing.T) {
		r, cache, got := newResolver(MismatchSource)
		body, err := r.Resolve(ref)
		if err != nil || string(body) != "moved" {
			t.Fatalf("got %q, %v; want the source's content", body, err)
		}
		if string(cache[CacheKey(ref)]) != "moved" {
			t.Error("adopting must retain what was adopted, or the next run mismatches again")
		}
		if !got.Violated {
			t.Error("adopting is still a violation worth reporting")
		}
	})

	t.Run("retained keeps what we had", func(t *testing.T) {
		r, cache, got := newResolver(MismatchRetained)
		body, err := r.Resolve(ref)
		if err != nil || string(body) != "retained" {
			t.Fatalf("got %q, %v; want the retained copy", body, err)
		}
		if string(cache[CacheKey(ref)]) != "retained" {
			t.Error("keeping ours must not overwrite it with the source's")
		}
		if !got.Violated {
			t.Error("keeping ours is still a violation worth reporting")
		}
	})

	// Unset must behave as fail: a policy that silently picked a side would decide for
	// an operator who never said which side they trust.
	t.Run("unset defaults to fail", func(t *testing.T) {
		r, _, _ := newResolver("")
		if _, err := r.Resolve(ref); err == nil {
			t.Fatal("want the default to stop")
		}
	})

	// A tracked reference asserts nothing, so policy never applies to it.
	t.Run("policy does not touch a tracked reference", func(t *testing.T) {
		tref := Parse("src//p.yml@refs/heads/main")
		r := Resolver{
			Fetcher:    &revFetcher{body: []byte("newer")},
			Cache:      mapCache{CacheKey(tref): []byte("older")},
			OnMismatch: MismatchFail,
		}
		body, err := r.Resolve(tref)
		if err != nil || string(body) != "newer" {
			t.Fatalf("got %q, %v; tracked must adopt regardless of policy", body, err)
		}
	})
}

func TestMismatchPolicyValidation(t *testing.T) {
	for _, p := range []MismatchPolicy{"", MismatchFail, MismatchSource, MismatchRetained} {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []MismatchPolicy{"warn", "ignore", "Fail"} {
		if p.Valid() {
			t.Errorf("%q should not be valid", p)
		}
	}
}
