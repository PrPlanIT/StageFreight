package retention

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// DefaultIdentityVars classifies a tag template's variables. IDENTITY vars name
// which independent series a tag belongs to — different values are separate lines
// that must never prune each other (a branch's tags never evict another branch's).
// Every other var is a SEQUENCE var: the axis a single series accumulates along
// (different values are points on one timeline). Only identity vars — these plus a
// per-policy `identity:` override — partition tags into groups.
var DefaultIdentityVars = map[string]bool{
	"branch": true,
	"env":    true,
}

// effectiveIdentity merges the default identity vars with a per-policy override.
func effectiveIdentity(extra []string) map[string]bool {
	id := make(map[string]bool, len(DefaultIdentityVars)+len(extra))
	for k := range DefaultIdentityVars {
		id[k] = true
	}
	for _, e := range extra {
		if e = strings.TrimSpace(e); e != "" {
			id[e] = true
		}
	}
	return id
}

// tmplMatcher is a compiled tag template: a capture regex, the capture-group names
// that are identity dimensions, and whether the template has any sequence var.
type tmplMatcher struct {
	template string
	re       *regexp.Regexp
	idGroups []string // capture-group names that are identity dimensions
	hasSeq   bool     // has ≥1 sequence var → an accumulating series (else rolling)
	negate   bool     // "!"-prefixed template — shapes candidacy only, never a group
}

// compileTemplate turns "test-{branch}-{sha:8}" into a matcher. Each {var} becomes
// a named `.+` capture; identity vars are recorded in idGroups, any sequence var
// sets hasSeq. A template with NO sequence var is ROLLING — a single value
// overwritten in place (e.g. "latest-dev"), which has nothing to prune along.
//
// Groups use greedy `.+`: for "test-{branch}-{sha}" against "test-a-b-00ff", the
// trailing sequence segment binds after the last literal separator (sha="00ff",
// branch="a-b"), which is correct for well-formed templates (identity before
// sequence, sequence last).
func compileTemplate(tmpl string, identity map[string]bool) (tmplMatcher, error) {
	m := tmplMatcher{template: tmpl}
	s := tmpl
	if strings.HasPrefix(s, "!") {
		m.negate = true
		s = s[1:]
	}
	var b strings.Builder
	b.WriteString("^")
	gi := 0
	for i := 0; i < len(s); {
		if s[i] == '{' {
			j := strings.IndexByte(s[i:], '}')
			if j < 0 {
				return m, fmt.Errorf("retention: unterminated '{' in template %q", tmpl)
			}
			inner := s[i+1 : i+j]
			name := inner
			if k := strings.IndexByte(inner, ':'); k >= 0 {
				name = inner[:k]
			}
			g := fmt.Sprintf("g%d", gi)
			gi++
			if identity[name] {
				m.idGroups = append(m.idGroups, g)
			} else {
				m.hasSeq = true
			}
			b.WriteString("(?P<" + g + ">.+)")
			i += j + 1
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(s[i])))
		i++
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return m, fmt.Errorf("retention: compiling template %q: %w", tmpl, err)
	}
	m.re = re
	return m, nil
}

// groupKey returns a stable key for the (template, identity-values) group of name.
// matched is false if name does not match this template at all. The key combines
// the template with each identity capture, so two tags share a group iff they came
// from the same template with the same identity values (same branch, same env, …).
func (m tmplMatcher) groupKey(name string) (key string, matched bool) {
	sub := m.re.FindStringSubmatch(name)
	if sub == nil {
		return "", false
	}
	key = m.template
	names := m.re.SubexpNames()
	for _, idn := range m.idGroups {
		for gi, gn := range names {
			if gn == idn {
				key += "\x00" + sub[gi]
			}
		}
	}
	return key, true
}

// newestOf returns the most recent CreatedAt across items (zero if empty).
func newestOf(items []Item) time.Time {
	var t time.Time
	for _, it := range items {
		if it.CreatedAt.After(t) {
			t = it.CreatedAt
		}
	}
	return t
}
