package stencil

import (
	"strconv"
	"testing"
)

func env() Env {
	e := MapEnv(
		map[string]string{"name": "stagefreight", "empty": "", "shipped": "6 images"},
		map[string]bool{"on": true, "off": false},
	)
	return e
}

func TestRender_Element(t *testing.T) {
	if got := Render("hi {name}, {empty}shipped {shipped}", env()); got != "hi stagefreight, shipped 6 images\n" {
		t.Fatalf("got %q", got)
	}
}

// One embed form: a scalar fact and a composed element read identically, and an
// unknown token is left LITERAL so a typo is visible.
func TestRender_UnknownLiteral(t *testing.T) {
	if got := Render("a={name} b={nope}", env()); got != "a=stagefreight b={nope}\n" {
		t.Fatalf("got %q", got)
	}
}

// A resolver that yields "" renders nothing (still consumed); the stencil never breaks.
func TestRender_EmptyResolves(t *testing.T) {
	if got := Render("[{empty}]", env()); got != "[]\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_ConditionalIfElse(t *testing.T) {
	if got := Render("{#if on}A{#else}B{/if}-{#if off}C{#else}D{/if}", env()); got != "A-D\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_ConditionalNoElseDropped(t *testing.T) {
	if got := Render("x{#if off}Y{/if}z", env()); got != "xz\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_ConditionalNested(t *testing.T) {
	tmpl := "{#if on}outer {#if off}io{#else}ie{/if} tail{/if}"
	if got := Render(tmpl, env()); got != "outer ie tail\n" {
		t.Fatalf("got %q", got)
	}
}

// Unbalanced conditional must not lose text or panic — emitted literally.
func TestRender_UnbalancedLiteral(t *testing.T) {
	if got := Render("before {#if on}dangling", env()); got != "before {#if on}dangling\n" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_CollapsesBlankLines(t *testing.T) {
	if got := Render("a\n\n\n\nb", env()); got != "a\n\nb\n" {
		t.Fatalf("got %q", got)
	}
}

// Nil closures degrade gracefully rather than panicking.
func TestRender_NilEnvSafe(t *testing.T) {
	if got := Render("x {y} {#if z}q{/if}", Env{}); got != "x {y}\n" {
		t.Fatalf("got %q", got)
	}
}

// An element resolves at most once per render: an impure/non-idempotent element
// (the ollama case) referenced twice must yield the SAME text both times — one
// element, one value per run — and the resolver is invoked exactly once.
func TestRender_ResolvesOncePerRender(t *testing.T) {
	calls := 0
	e := Env{Resolve: func(name string) (string, bool) {
		if name == "n" {
			calls++
			return strconv.Itoa(calls), true
		}
		return "", false
	}}
	got := Render("{n} and {n} and {n}", e)
	if got != "1 and 1 and 1\n" {
		t.Fatalf("references not stable within a render: %q", got)
	}
	if calls != 1 {
		t.Fatalf("resolver invoked %d times; want 1 (memoized per render)", calls)
	}
}

// {{ … }} is a gitver literal-escape: stencil must NOT resolve it, and must pass it
// through untouched so the downstream leaf fact-pass unescapes it to a literal.
func TestRender_DoubleBraceEscaped(t *testing.T) {
	e := MapEnv(map[string]string{"build": "BADGE", "sha": "SHA"}, nil)
	if got := Render("{build}", e); got != "BADGE\n" {
		t.Fatalf("single brace should resolve: %q", got)
	}
	if got := Render("{{build}}", e); got != "{{build}}\n" {
		t.Fatalf("double brace must pass through literal: %q", got)
	}
	if got := Render("{{sha}} vs {build}", e); got != "{{sha}} vs BADGE\n" {
		t.Fatalf("escaped stays, bare resolves: %q", got)
	}
}

// An element that resolves to empty leaves no stray separator space — but a mid-prose
// empty must NOT eat a real space (e.g. a comma's).
func TestRender_EmptyElidesSeparatorSpace(t *testing.T) {
	e := MapEnv(map[string]string{"a": "A", "b": "", "c": "C"}, nil)
	cases := []struct{ in, want string }{
		{"{a} {b} {c}", "A C\n"}, // mid-sequence: following space consumed
		{"{a} {b}", "A\n"},       // trailing empty: preceding space consumed
		{"x {b} y", "x y\n"},     // single following space consumed
		{"{b} {c}", "C\n"},       // leading empty: following space consumed
		{"## H {b}", "## H\n"},   // trailing empty at end-of-line
		{"a, {b}c", "a, c\n"},    // mid-prose: the comma's space is preserved
	}
	for _, tc := range cases {
		if got := Render(tc.in, e); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

// The engine is pure: the same template + Env renders byte-identical output.
func TestRender_Deterministic(t *testing.T) {
	tmpl := "{name}: {#if on}{shipped}{#else}{empty}{/if}"
	if a, b := Render(tmpl, env()), Render(tmpl, env()); a != b {
		t.Fatalf("not deterministic:\n a=%q\n b=%q", a, b)
	}
}
