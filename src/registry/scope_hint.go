package registry

import (
	"fmt"
	"strings"
)

// ScopeRequirement lists the scopes/permissions an operation needs on a provider.
// Publish is carried alongside Prune so a remediation message can tell the operator
// what to ADD without nudging them to drop a scope another operation still needs
// (the forge only sees the one rejected call; StageFreight knows the credential's
// full role across the pipeline).
type ScopeRequirement struct {
	Prune   []string // scopes needed to delete tags/versions (retention)
	Publish []string // scopes needed to push (kept in the message so it isn't stripped)
	Known   bool     // false → no scope model for this provider; caller shows a generic line
}

// RequiredScopes returns scope requirements keyed on the raw Provider() string
// (ghcr/docker/harbor/quay/…, NOT the normalized alias). Only providers whose scope
// names are known for certain are marked Known — a wrong scope name in a remediation
// message is worse than none, so unknowns fall back to a generic message.
func RequiredScopes(provider string) ScopeRequirement {
	switch provider {
	case "ghcr", "github":
		return ScopeRequirement{
			Prune:   []string{"read:packages", "delete:packages"},
			Publish: []string{"write:packages"},
			Known:   true,
		}
	// Extend as each provider's scope names are confirmed:
	//   docker/dockerhub, harbor, quay, gitlab, gitea, jfrog.
	default:
		return ScopeRequirement{}
	}
}

// ScopeDeniedMessage renders a one-line, plain-English remediation for a batch of
// deletes rejected for lack of permission (e.g. a 403 during prune). It names the
// credential and states what each operation requires — never implying a scope should
// be removed. n is how many tags were blocked by the single underlying cause.
func ScopeDeniedMessage(provider, credential string, n int) string {
	name := credential
	if name == "" {
		name = "the registry token"
	}
	tags := fmt.Sprintf("%d tag", n)
	if n != 1 {
		tags += "s"
	}
	req := RequiredScopes(provider)
	if !req.Known {
		return fmt.Sprintf("403 Error — %s not pruned — %s lacks delete permission on %s", tags, name, provider)
	}
	return fmt.Sprintf("403 Error — %s not pruned — %s has insufficient scope; prune requires %s (publish requires %s)",
		tags, name, quoteScopes(req.Prune), quoteScopes(req.Publish))
}

func quoteScopes(s []string) string {
	q := make([]string, len(s))
	for i, v := range s {
		q[i] = `"` + v + `"`
	}
	return strings.Join(q, " ")
}
