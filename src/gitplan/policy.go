package gitplan

import "path"

// Policy is the small, declarative "what is allowed" data — NOT a rules engine. The
// planner holds the intelligence; policy holds a protected-destination list (and, later,
// branch classes). Deliberately almost boring: no expression language, ever.
type Policy struct {
	Protected []string // glob patterns for protected destinations, e.g. "main", "release/*"
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
