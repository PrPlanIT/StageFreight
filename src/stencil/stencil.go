// Package stencil is StageFreight's audience-text composition engine — the one
// lever for every place SF emits text a human reads as the project's voice
// (scribe README regions, narrate notifications, release bodies, and later tag/
// commit messages). A stencil is a reusable pattern SF STAMPS, filling it per run:
// freeform markdown you author, with {element} embeds where SF supplies text.
//
// The design line it enforces: SF owns the FACTS (a resolver yields the changelog,
// the digest, the status — structured and trustworthy), the author owns the
// FRAMING (the prose, order, and voice around them). Defaults live underneath —
// omit a stencil and SF stamps its embedded default; author one and you've seized
// the pen.
//
// Grammar — deliberately one embed form, so a badge, a fact, and a composed
// stencil all read identically and nothing is a special citizen:
//
//	{name} — an element; replaced by env.Resolve(name)
//
// There are NO conditionals — conditional logic does not belong in a templating
// system. Alternation lives in status-carrying facts (an element that resolves
// differently on pass/fail); absence lives in elision (empty token → gone; a line
// whose every token resolved empty → the whole line gone); a different ARC (pass vs
// fail message) is a different stencil selected by routing. Those three mechanisms
// cover everything a conditional would, without the body ever branching.
//
// The engine is PURE and deterministic: the same template + Env renders
// byte-identical output (no time, no map-iteration order, no ambient state) — the
// same reproducibility discipline the badge subset enforces. Graceful by design: a
// resolver that yields "" renders nothing, and a LINE whose every embed resolved
// empty renders nothing at all (line elision — authored labels vanish with their
// data, so "no data → the line disappears" needs no conditionals); an unknown token
// is left LITERAL — and keeps its line — so a typo is visible, never silently
// swallowed. A stencil never breaks and the engine never fabricates.
package stencil

import "strings"

// Env resolves the elements a stencil references. A consumer (scribe, the narrate
// runner, release) supplies this closure over its own facts, so the engine stays
// vocabulary-agnostic — it knows the grammar, never the nouns.
type Env struct {
	// Resolve returns the text an element name expands to, and whether it is known.
	// A composed stencil embedding other elements resolves them by calling Render
	// itself (composition = resolver recursion), so the engine never re-scans
	// expanded output — a fact whose text happens to contain braces is never
	// re-interpreted.
	Resolve func(name string) (string, bool)
}

// MapEnv builds an Env from a plain map — handy for tests and simple callers.
func MapEnv(vars map[string]string) Env {
	return Env{
		Resolve: func(name string) (string, bool) { v, ok := vars[name]; return v, ok },
	}
}

// Render substitutes {element} embeds line by line (with line elision), then tidies
// runs of blank lines. Deterministic for pure resolvers.
//
// Each element resolves AT MOST ONCE per render (memoized by name): an impure
// element — an ollama transform is the motivating case — referenced twice must
// yield the SAME text both times, or one named element would mean two things in a
// single document. So an impure element is one value per run, stable wherever it
// appears (non-deterministic across runs, consistent within one). Expensive facts
// (the changelog) likewise aren't recomputed per reference.
func Render(tmpl string, env Env) string {
	return collapseBlankLines(Expand(tmpl, env))
}

// Expand is Render WITHOUT the blank-line tidy: it resolves {element} embeds (with
// the same per-render memoization and line elision) but preserves the resolved text
// verbatim otherwise. Use it where element content must survive byte-for-byte —
// scribe file bodies, where a multi-line element (an included doc, a table) must
// not have its internal blank runs squeezed, and no terminating newline should be
// forced. Render layers collapseBlankLines on top for audience summaries, where
// elided lines would otherwise leave ragged gaps.
func Expand(tmpl string, env Env) string {
	base := env.Resolve
	if base == nil {
		base = func(string) (string, bool) { return "", false }
	}
	type memoVal struct {
		v  string
		ok bool
	}
	memo := map[string]memoVal{}
	resolve := func(name string) (string, bool) {
		if m, seen := memo[name]; seen {
			return m.v, m.ok
		}
		v, ok := base(name)
		memo[name] = memoVal{v, ok}
		return v, ok
	}
	return substituteInline(tmpl, resolve)
}

// substituteInline replaces {name} embeds line by line, with LINE ELISION: a line
// whose every embed resolved EMPTY renders nothing at all — authored text included —
// so `Shipped {publish.tags} → {publish.registries}` vanishes whole when publish
// recorded nothing, never leaving a dangling "Shipped → ". This is what lets a body
// keep its labels/prose next to elidable facts (the wording stays in the body; no
// phrase-shaped facts). A line containing an UNKNOWN token is always kept (the
// literal token is a visible typo — visibility beats tidiness), as is any line with
// a non-empty resolution or no embeds at all. Tokens never span lines.
func substituteInline(s string, resolve func(string) (string, bool)) string {
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		nl := strings.IndexByte(s, '\n')
		var line string
		hasNL := nl >= 0
		if hasNL {
			line, s = s[:nl], s[nl+1:]
		} else {
			line, s = s, ""
		}
		out, nonEmpty, empty, unknown := substituteLine(line, resolve)
		if empty > 0 && nonEmpty == 0 && unknown == 0 {
			continue // line elision: every token on the line resolved empty
		}
		b.WriteString(out)
		if hasNL {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// substituteLine replaces {name} embeds in one line, left-to-right. Resolved output
// is appended directly and never re-scanned. An unknown token is left literal
// (visible typo, not a silent drop). Returns the token accounting substituteInline
// uses for line elision: non-empty resolutions, empty resolutions, unknown tokens.
func substituteLine(s string, resolve func(string) (string, bool)) (out string, nonEmpty, empty, unknown int) {
	var buf []byte
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return string(append(buf, s...)), nonEmpty, empty, unknown
		}
		buf = append(buf, s[:open]...)
		s = s[open:]
		// {{ … }} is a gitver literal-escape (src/gitver/escape.go), NOT an embed.
		// Emit the opening braces untouched and let the downstream leaf fact-pass
		// unescape them, so `{{sha}}` renders a literal `{sha}` rather than resolving
		// here. (Without this the escape survived only by accident of malformed-token
		// parsing.)
		if strings.HasPrefix(s, "{{") {
			buf = append(buf, '{', '{')
			s = s[2:]
			continue
		}
		close := strings.IndexByte(s, '}')
		if close < 0 { // dangling '{' — emit the remainder literally
			return string(append(buf, s...)), nonEmpty, empty, unknown
		}
		name := strings.TrimSpace(s[1:close])
		rendered, ok := resolve(name)
		if !ok {
			unknown++
			buf = append(buf, s[:close+1]...) // unknown token — keep literal
			s = s[close+1:]
			continue
		}
		if rendered == "" {
			empty++
			// An element that resolves to EMPTY must leave no stray separator space.
			// Consume one FOLLOWING horizontal space (the common mid-sequence case:
			// `{a} {b} {c}` with b empty → "A C"), or — for a trailing empty embed at
			// end-of-line — one PRECEDING space (`## Changes {range}` → "## Changes").
			// A mid-prose empty with non-space neighbors is left alone, so `a, {x}b`
			// stays "a, b" and never eats the comma's space.
			rest := s[close+1:]
			switch {
			case len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t'):
				s = rest[1:]
			case len(rest) == 0 && len(buf) > 0 && (buf[len(buf)-1] == ' ' || buf[len(buf)-1] == '\t'):
				buf = buf[:len(buf)-1]
				s = rest
			default:
				s = rest
			}
			continue
		}
		nonEmpty++
		buf = append(buf, rendered...)
		s = s[close+1:]
	}
}

// collapseBlankLines squeezes 3+ consecutive newlines to exactly 2, so dropped
// conditionals and empty elements don't leave ragged gaps. Deterministic.
func collapseBlankLines(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	newlines := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			newlines++
			if newlines <= 2 {
				b.WriteByte('\n')
			}
			continue
		}
		newlines = 0
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String()) + "\n"
}
