package docker

import (
	"fmt"
	"strings"
)

// CacheTag is a deterministic, repo-scoped cache tag.
// Format: <prefix>-<repo-hash-8>-<branch-canonical>
// where branch-canonical = <normalized-branch>-<branch-hash-8>
//
// This is the ONLY way to construct or validate cache tags.
// Both the writer (BuildCacheFlags) and retention matcher must use this.
// Changing this format is a compatibility boundary.
type CacheTag struct {
	Prefix    string // e.g., "cache"
	RepoHash  string // 8-char repo scope hash
	BranchRef string // canonicalized branch (from CanonicalizeRef)
}

// BuildCacheTag constructs a cache tag from repo + branch identifiers.
// This is the single constructor — no other code should build cache tag strings.
func BuildCacheTag(prefix, repoID, branch string) CacheTag {
	if prefix == "" {
		prefix = "cache"
	}
	return CacheTag{
		Prefix:    prefix,
		RepoHash:  repoHash(repoID)[:8],
		BranchRef: CanonicalizeRef(branch),
	}
}

// String renders the full tag string for registry use.
func (t CacheTag) String() string {
	return fmt.Sprintf("%s-%s-%s", t.Prefix, t.RepoHash, t.BranchRef)
}

// ScopePrefix returns the repo-scoped prefix for retention matching.
// All tags for this repo start with this prefix.
func (t CacheTag) ScopePrefix() string {
	return fmt.Sprintf("%s-%s", t.Prefix, t.RepoHash)
}

// ParseCacheTag parses a tag string into a CacheTag.
// Returns nil if the tag doesn't match StageFreight's deterministic naming.
// This is the inverse of BuildCacheTag — constructor + parser pair.
func ParseCacheTag(tag string) *CacheTag {
	// Format: <prefix>-<repo-hash-8>-<branch-name>-<branch-hash-8>
	// We need to find the prefix, repo hash, and branch ref.
	// Strategy: split from right to find the branch hash (last 8 hex),
	// then the repo hash (8 hex after prefix), leaving prefix and branch name.

	// Must end with -<8 hex chars> (branch hash from CanonicalizeRef).
	lastDash := strings.LastIndex(tag, "-")
	if lastDash < 0 || lastDash == len(tag)-1 {
		return nil
	}
	branchHash := tag[lastDash+1:]
	if !isHex8(branchHash) {
		return nil
	}

	// Everything before the branch hash is <prefix>-<repo-hash>-<branch-name>.
	rest := tag[:lastDash]

	// Find repo hash: scan for an 8-char hex segment.
	// The tag is <prefix>-<repo-hash-8>-<branch-name>.
	// We split on "-" and look for the first 8-char hex segment after at least one prefix segment.
	parts := strings.Split(rest, "-")
	repoIdx := -1
	for i := 1; i < len(parts); i++ {
		if isHex8(parts[i]) {
			repoIdx = i
			break
		}
	}
	if repoIdx < 1 {
		return nil
	}

	prefix := strings.Join(parts[:repoIdx], "-")
	repoHash := parts[repoIdx]
	branchName := ""
	if repoIdx+1 < len(parts) {
		branchName = strings.Join(parts[repoIdx+1:], "-")
	}

	return &CacheTag{
		Prefix:    prefix,
		RepoHash:  repoHash,
		BranchRef: branchName + "-" + branchHash,
	}
}

// MatchesScope returns true if this tag belongs to the given repo scope.
func (t CacheTag) MatchesScope(scopePrefix string) bool {
	return t.ScopePrefix() == scopePrefix
}

// IsSFCacheTag validates that a tag string matches StageFreight's deterministic
// cache naming pattern for the given scope prefix. Uses ParseCacheTag internally.
func IsSFCacheTag(tag, scopePrefix string) bool {
	parsed := ParseCacheTag(tag)
	if parsed == nil {
		return false
	}
	return parsed.MatchesScope(scopePrefix)
}

func isHex8(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
