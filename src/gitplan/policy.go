package gitplan

import "path"

// Policy is the small, declarative "what is allowed" data — NOT a rules engine. The
// planner holds the intelligence; policy holds a protected-destination list (and, later,
// branch classes). Deliberately almost boring: no expression language, ever.
type Policy struct {
	Protected []string // glob patterns for protected destinations, e.g. "main", "release/*"
}

// DefaultPolicy is the fallback protected set when a repo declares none. Callers that have
// a config-provided list should prefer it; this keeps the sensible-default in one place
// instead of hardcoded at each call site.
func DefaultPolicy() Policy {
	return Policy{Protected: []string{"main", "master"}}
}

// WithProtected returns a Policy from a config-provided protected list, falling back to the
// default when the list is empty.
func WithProtected(protected []string) Policy {
	if len(protected) == 0 {
		return DefaultPolicy()
	}
	return Policy{Protected: protected}
}

// IsProtected reports whether a branch name matches any protected pattern.
func (p Policy) IsProtected(branch string) bool {
	for _, pat := range p.Protected {
		if ok, _ := path.Match(pat, branch); ok {
			return true
		}
	}
	return false
}

// DivergeRule returns the policy-resolved handling of a diverged NON-protected
// destination. Default: DivergeAsk — user-intent ambiguity (pull vs rebase vs force) is
// not policy's to resolve. Grows from real need, not speculation.
func (p Policy) DivergeRule(branch string) DivergeRule {
	return DivergeAsk
}
