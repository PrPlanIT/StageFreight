package stencil

import (
	"strconv"
	"testing"
)

func env() Env {
	return MapEnv(map[string]string{"name": "stagefreight", "empty": "", "shipped": "6 images"})
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

// A resolver that yields "" renders nothing — and a line whose EVERY embed resolved
// empty drops whole, authored text included (line elision): the label vanishes with
// its data, so "no data → the line disappears" needs no conditionals.
func TestRender_EmptyResolves(t *testing.T) {
	if got := Render("[{empty}]", env()); got != "\n" {
		t.Fatalf("all-empty line should elide: got %q", got)
	}
}

// Line elision accounting: an all-empty-token line drops; any non-empty resolution
// or unknown-literal token keeps the line (visibility beats tidiness); a line with
// no embeds at all is never elided.
func TestRender_LineElision(t *testing.T) {
	e := MapEnv(map[string]string{"a": "A", "b": ""})
	cases := []struct{ in, want string }{
		{"one\nShipped {b} → {b}\ntwo", "one\ntwo\n"},            // label + all-empty → whole line gone
		{"one\nShipped {a} → {b}\ntwo", "one\nShipped A\ntwo\n"}, // non-empty keeps the line; the orphaned joiner goes with the empty
		{"one\n{b} {nope}\ntwo", "one\n{nope}\ntwo\n"},           // unknown literal keeps the line
		{"just text\n{b}", "just text\n"},                        // token-only empty line drops
		{"{{a}}\n{b}", "{{a}}\n"},                                // escape is authored text, never elides
	}
	for _, tc := range cases {
		if got := Render(tc.in, e); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

// There are NO conditionals in the grammar: a {#if …} sequence is just unknown
// tokens, left literal so the retired syntax is VISIBLE in output, never silently
// interpreted or swallowed.
func TestRender_NoConditionalGrammar(t *testing.T) {
	if got := Render("x {#if on}Y{/if} z", env()); got != "x {#if on}Y{/if} z\n" {
		t.Fatalf("retired conditional syntax must stay literal: got %q", got)
	}
}

func TestRender_CollapsesBlankLines(t *testing.T) {
	if got := Render("a\n\n\n\nb", env()); got != "a\n\nb\n" {
		t.Fatalf("got %q", got)
	}
}

// Nil closures degrade gracefully rather than panicking.
func TestRender_NilEnvSafe(t *testing.T) {
	if got := Render("x {y}", Env{}); got != "x {y}\n" {
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
	e := MapEnv(map[string]string{"build": "BADGE", "sha": "SHA"})
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

// An element that resolves to empty leaves no stray separator space on a line that
// SURVIVES elision (some other embed resolved non-empty) — and a mid-prose empty
// must NOT eat a real space (e.g. a comma's).
func TestRender_EmptyElidesSeparatorSpace(t *testing.T) {
	e := MapEnv(map[string]string{"a": "A", "b": "", "c": "C"})
	cases := []struct{ in, want string }{
		{"{a} {b} {c}", "A C\n"},   // mid-sequence: following space consumed
		{"{a} {b}", "A\n"},         // trailing empty: preceding space consumed
		{"{a} x {b} y", "A x y\n"}, // single following space consumed
		{"{b} {c}", "C\n"},         // leading empty: following space consumed
		{"## H {b} {c}", "## H C\n"},
		{"{a}, {b}c", "A, c\n"},        // mid-prose: the comma's space is preserved
		{"{a} · {b}", "A\n"},           // trailing empty takes its joiner glyph with it
		{"{a} — {b}", "A\n"},           //
		{"v{a}: {b}", "vA\n"},          // orphaned colon goes too
		{"{a} · {c} · {b}", "A · C\n"}, // only the LAST joiner is orphaned
	}
	for _, tc := range cases {
		if got := Render(tc.in, e); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}

// The engine is pure: the same template + Env renders byte-identical output.
func TestRender_Deterministic(t *testing.T) {
	tmpl := "{name}: {shipped} {empty}"
	if a, b := Render(tmpl, env()), Render(tmpl, env()); a != b {
		t.Fatalf("not deterministic:\n a=%q\n b=%q", a, b)
	}
}
