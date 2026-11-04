package workflow

import (
	"fmt"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/forge"
)

// Role is what you act as on a repo, lowest to highest.
type Role int

const (
	Audience Role = iota // watch, comment, triage; no push
	Talent               // push working branches, open merge requests
	Director             // merge and ship within the rules, orchestrate promotion
	Producer             // policy authority: cut releases, override the rules
)

var roleNames = []string{"audience", "talent", "director", "producer"}

func (r Role) String() string {
	if int(r) < len(roleNames) {
		return roleNames[r]
	}
	return fmt.Sprintf("role(%d)", int(r))
}

// ParseRole reads a role name.
func ParseRole(s string) (Role, error) {
	for i, n := range roleNames {
		if strings.EqualFold(s, n) {
			return Role(i), nil
		}
	}
	return Audience, fmt.Errorf("unknown role %q (%s)", s, strings.Join(roleNames, " | "))
}

// RoleFor maps a forge access level to the role it grants.
func RoleFor(level forge.AccessLevel) Role {
	switch level {
	case forge.AccessAdmin:
		return Producer
	case forge.AccessMaintain:
		return Director
	case forge.AccessWrite:
		return Talent
	}
	return Audience
}

// Standing is the role you act as, and the ceiling it is bounded by.
type Standing struct {
	Role Role
	// Ceiling is the role your forge access grants; Known is false when it couldn't be read
	// (offline, no token, a forge that can't report it), and the role is then unbounded —
	// the forge still enforces for real on push.
	Ceiling Role
	Known   bool
}

// Effective resolves the role you act as. Everyone acts as talent unless they ask for a
// role with --role, so reaching a higher role is a deliberate act; requested can only lower
// a known ceiling, never raise it.
func Effective(ceiling Role, known bool, requested string) (Standing, error) {
	s := Standing{Ceiling: ceiling, Known: known, Role: Talent}
	if requested != "" {
		r, err := ParseRole(requested)
		if err != nil {
			return s, err
		}
		if known && r > ceiling {
			return s, fmt.Errorf("--role %s: your access on this repo makes you %s, and --role can only lower that", r, ceiling)
		}
		s.Role = r
	} else if known && ceiling < s.Role {
		s.Role = ceiling
	}
	return s, nil
}

// PushRole checks that your role may push to branch: anyone below talent may not push at
// all, and a long-lived branch that takes direct pushes (a trunk, a release-train channel)
// is shared state that a director moves.
func PushRole(cfg *config.Config, branch string, s Standing) *Finding {
	if cfg == nil || !cfg.Workflow.Present() || branch == "" {
		return nil
	}
	need, why := Talent, "pushing"
	if c, ok := ClassOf(cfg, branch); ok && c.Enforcement != config.EnforcementOff &&
		(c.Lifetime == "" || c.Lifetime == config.LifetimeLong) && c.Landing != config.LandingMergeRequest {
		need, why = Director, fmt.Sprintf("pushing straight to %s, a long-lived %s branch,", branch, c.ID)
	}
	return roleFinding(s, need, why)
}

// ReleaseRole checks that your role may cut a release tag: greenlighting a release is a
// producer's call.
func ReleaseRole(cfg *config.Config, s Standing) *Finding {
	if cfg == nil || !cfg.Workflow.Present() {
		return nil
	}
	return roleFinding(s, Producer, "cutting a release")
}

// CanOverride reports whether your role may override a workflow refusal.
func CanOverride(s Standing) bool { return s.Role >= Producer }

func roleFinding(s Standing, need Role, why string) *Finding {
	if s.Role >= need {
		return nil
	}
	teach := fmt.Sprintf("re-run with --role %s", need)
	if s.Known && s.Ceiling < need {
		teach = fmt.Sprintf("your access on this repo makes you %s; ask a %s", s.Ceiling, need)
	}
	return &Finding{
		Rule:    "role",
		Message: fmt.Sprintf("%s needs the %s role; you're acting as %s", why, need, s.Role),
		Teach:   teach,
	}
}
