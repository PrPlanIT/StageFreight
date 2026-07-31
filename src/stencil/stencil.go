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
//	{name}                          — an element; replaced by env.Resolve(name)
//	{#if cond} … {#else} … {/if}    — a region gated by env.Cond(cond)
//
// The engine is PURE and deterministic: the same template + Env renders
// byte-identical output (no time, no map-iteration order, no ambient state) — the
// same reproducibility discipline the badge subset enforces. Graceful by design: a
// resolver that yields "" renders nothing; an unknown token is left LITERAL so a
// typo is visible, never silently swallowed. A stencil never breaks and the engine
// never fabricates.
package stencil

import "strings"

// Env resolves the elements and conditionals a stencil references. A consumer
// (narrate, scribe, release) supplies these closures over its own facts, so the
// engine stays vocabulary-agnostic — it knows the grammar, never the nouns.
type Env struct {
	// Resolve returns the text an element name expands to, and whether it is known.
	// A composed stencil embedding other elements resolves them by calling Render
	// itself (composition = resolver recursion), so the engine never re-scans
	// expanded output — a fact whose text happens to contain braces is never
	// re-interpreted.
	Resolve func(name string) (string, bool)
	// Cond returns the truth of a conditional name (unknown → false).
	Cond func(name string) bool
}

// MapEnv builds an Env from plain maps — handy for tests and simple callers.
func MapEnv(vars map[string]string, conds map[string]bool) Env {
	return Env{
		Resolve: func(name string) (string, bool) { v, ok := vars[name]; return v, ok },
		Cond:    func(name string) bool { return conds[name] },
	}
}

// Render resolves conditionals, then substitutes {element} embeds in a single
// left-to-right pass, then tidies runs of blank lines. Deterministic for pure
// resolvers.
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

// Expand is Render WITHOUT the blank-line tidy: it resolves conditionals + {element}
// embeds (with the same per-render memoization) but preserves the resolved text
// verbatim. Use it where element content must survive byte-for-byte — scribe file
// bodies, where a multi-line element (an included doc, a table) must not have its
// internal blank runs squeezed, and no terminating newline should be forced. Render
// layers collapseBlankLines on top for narrate stories, where dropped conditionals
// and empty beats would otherwise leave ragged gaps.
func Expand(tmpl string, env Env) string {
	cond := env.Cond
	if cond == nil {
		cond = func(string) bool { return false }
	}
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
	resolved := resolveConditionals(tmpl, cond)
	return substituteInline(resolved, resolve)
}

// resolveConditionals expands every {#if cond} … {#else} … {/if} block, keeping
// the taken branch and recursing into it so nested conditionals resolve too. An
// unbalanced/malformed block is emitted literally rather than panicking.
func resolveConditionals(s string, cond func(string) bool) string {
	const openTag = "{#if "
	var b strings.Builder
	for {
		open := strings.Index(s, openTag)
		if open < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:open])

		nameEnd := strings.Index(s[open:], "}")
		if nameEnd < 0 { // malformed opener — emit the rest literally
			b.WriteString(s[open:])
			return b.String()
		}
		nameEnd += open
		name := strings.TrimSpace(s[open+len(openTag) : nameEnd])

		body, after, ok := splitIfBlock(s[nameEnd+1:])
		if !ok { // no matching {/if} — emit literally, don't lose text
			b.WriteString(s[open:])
			return b.String()
		}

		ifBranch, elseBranch := splitElse(body)
		kept := elseBranch
		if cond(name) {
			kept = ifBranch
		}
		b.WriteString(resolveConditionals(kept, cond))
		s = after
	}
}

// splitIfBlock returns the text between an already-consumed {#if …} and its
// MATCHING {/if} (honoring nested {#if …}), plus everything after {/if}.
func splitIfBlock(s string) (body, after string, ok bool) {
	const openTag, closeTag = "{#if ", "{/if}"
	depth := 0
	i := 0
	for i < len(s) {
		nextOpen := strings.Index(s[i:], openTag)
		nextClose := strings.Index(s[i:], closeTag)
		if nextClose < 0 {
			return "", "", false // unbalanced
		}
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			i += nextOpen + len(openTag)
			continue
		}
		if depth == 0 {
			absClose := i + nextClose
			return s[:absClose], s[absClose+len(closeTag):], true
		}
		depth--
		i += nextClose + len(closeTag)
	}
	return "", "", false
}

// splitElse splits a conditional body at its top-level {#else} (depth 0). With no
// else, the else-branch is empty.
func splitElse(body string) (ifBranch, elseBranch string) {
	const openTag, elseTag, closeTag = "{#if ", "{#else}", "{/if}"
	depth := 0
	i := 0
	for i < len(body) {
		nextOpen := strings.Index(body[i:], openTag)
		nextElse := strings.Index(body[i:], elseTag)
		nextClose := strings.Index(body[i:], closeTag)
		if nextElse >= 0 && depth == 0 &&
			(nextOpen < 0 || nextElse < nextOpen) &&
			(nextClose < 0 || nextElse < nextClose) {
			return body[:i+nextElse], body[i+nextElse+len(elseTag):]
		}
		if nextOpen >= 0 && (nextClose < 0 || nextOpen < nextClose) {
			depth++
			i += nextOpen + len(openTag)
			continue
		}
		if nextClose >= 0 {
			if depth > 0 {
				depth--
			}
			i += nextClose + len(closeTag)
			continue
		}
		break
	}
	return body, ""
}

// substituteInline replaces {name} embeds in one left-to-right pass. Resolved
// output is appended directly and never re-scanned. An unknown token is left
// literal (visible typo, not a silent drop).
func substituteInline(s string, resolve func(string) (string, bool)) string {
	var buf []byte
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return string(append(buf, s...))
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
			return string(append(buf, s...))
		}
		name := strings.TrimSpace(s[1:close])
		rendered, ok := resolve(name)
		if !ok {
			buf = append(buf, s[:close+1]...) // unknown token — keep literal
			s = s[close+1:]
			continue
		}
		if rendered == "" {
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
			case (len(rest) == 0 || rest[0] == '\n') && len(buf) > 0 && (buf[len(buf)-1] == ' ' || buf[len(buf)-1] == '\t'):
				buf = buf[:len(buf)-1]
				s = rest
			default:
				s = rest
			}
			continue
		}
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
