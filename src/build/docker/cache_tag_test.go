package docker

import (
	"testing"
)

func TestBuildCacheTag_RepoIsolation(t *testing.T) {
	// Two repos, same branch → different tags (no collision on shared targets).
	tagA := BuildCacheTag("cache", "https://gitlab.com/org/repo-a", "main")
	tagB := BuildCacheTag("cache", "https://gitlab.com/org/repo-b", "main")

	if tagA.String() == tagB.String() {
		t.Errorf("shared target collision: repo-a and repo-b produce same tag %q", tagA.String())
	}
	if tagA.ScopePrefix() == tagB.ScopePrefix() {
		t.Errorf("shared target collision: repo-a and repo-b produce same scope prefix %q", tagA.ScopePrefix())
	}
}

func TestBuildCacheTag_BranchIsolation(t *testing.T) {
	// Same repo, different branches → different tags.
	tagA := BuildCacheTag("cache", "https://gitlab.com/org/repo", "main")
	tagB := BuildCacheTag("cache", "https://gitlab.com/org/repo", "develop")

	if tagA.String() == tagB.String() {
		t.Errorf("branch collision: main and develop produce same tag %q", tagA.String())
	}
	// Same repo → same scope prefix.
	if tagA.ScopePrefix() != tagB.ScopePrefix() {
		t.Errorf("scope prefix should match for same repo: %q vs %q", tagA.ScopePrefix(), tagB.ScopePrefix())
	}
}

func TestBuildCacheTag_Deterministic(t *testing.T) {
	// Same inputs → same output.
	a := BuildCacheTag("cache", "https://gitlab.com/org/repo", "main")
	b := BuildCacheTag("cache", "https://gitlab.com/org/repo", "main")
	if a.String() != b.String() {
		t.Errorf("not deterministic: %q vs %q", a.String(), b.String())
	}
}

func TestBuildCacheTag_DefaultPrefix(t *testing.T) {
	tag := BuildCacheTag("", "https://gitlab.com/org/repo", "main")
	if tag.Prefix != "cache" {
		t.Errorf("expected default prefix 'cache', got %q", tag.Prefix)
	}
}

func TestIsSFCacheTag_Valid(t *testing.T) {
	tag := BuildCacheTag("cache", "https://gitlab.com/org/repo", "main")
	if !IsSFCacheTag(tag.String(), tag.ScopePrefix()) {
		t.Errorf("IsSFCacheTag should match tag %q with prefix %q", tag.String(), tag.ScopePrefix())
	}
}

func TestIsSFCacheTag_WrongPrefix(t *testing.T) {
	tag := BuildCacheTag("cache", "https://gitlab.com/org/repo-a", "main")
	otherPrefix := BuildCacheTag("cache", "https://gitlab.com/org/repo-b", "main").ScopePrefix()
	if IsSFCacheTag(tag.String(), otherPrefix) {
		t.Errorf("IsSFCacheTag should NOT match tag %q with prefix %q (different repo)", tag.String(), otherPrefix)
	}
}

func TestIsSFCacheTag_RandomTag(t *testing.T) {
	prefix := BuildCacheTag("cache", "https://gitlab.com/org/repo", "main").ScopePrefix()
	// Random tags that don't follow the pattern.
	for _, tag := range []string{
		"latest",
		"v1.0.0",
		"cache-main",
		"cache-something-without-hash",
		prefix + "-no-hash-suffix",
	} {
		if IsSFCacheTag(tag, prefix) {
			t.Errorf("IsSFCacheTag should NOT match random tag %q", tag)
		}
	}
}

func TestParseCacheTag_Roundtrip(t *testing.T) {
	tag := BuildCacheTag("cache", "https://gitlab.com/org/repo", "main")
	parsed := ParseCacheTag(tag.String())
	if parsed == nil {
		t.Fatalf("ParseCacheTag returned nil for valid tag %q", tag.String())
	}
	if parsed.Prefix != tag.Prefix {
		t.Errorf("prefix: got %q want %q", parsed.Prefix, tag.Prefix)
	}
	if parsed.RepoHash != tag.RepoHash {
		t.Errorf("repo hash: got %q want %q", parsed.RepoHash, tag.RepoHash)
	}
	if parsed.BranchRef != tag.BranchRef {
		t.Errorf("branch ref: got %q want %q", parsed.BranchRef, tag.BranchRef)
	}
}

func TestParseCacheTag_FeatureBranch(t *testing.T) {
	tag := BuildCacheTag("cache", "https://gitlab.com/org/repo", "feature/my-branch")
	parsed := ParseCacheTag(tag.String())
	if parsed == nil {
		t.Fatalf("ParseCacheTag returned nil for feature branch tag %q", tag.String())
	}
	if parsed.BranchRef != tag.BranchRef {
		t.Errorf("branch ref: got %q want %q", parsed.BranchRef, tag.BranchRef)
	}
}

func TestParseCacheTag_Invalid(t *testing.T) {
	for _, tag := range []string{
		"",
		"latest",
		"v1.0.0",
		"cache-main",
		"no-hash-suffix-here",
	} {
		if parsed := ParseCacheTag(tag); parsed != nil {
			t.Errorf("ParseCacheTag should return nil for %q, got %+v", tag, parsed)
		}
	}
}

func TestMatchesScope(t *testing.T) {
	tagA := BuildCacheTag("cache", "https://gitlab.com/org/repo-a", "main")
	tagB := BuildCacheTag("cache", "https://gitlab.com/org/repo-b", "main")

	parsedA := ParseCacheTag(tagA.String())
	if parsedA == nil {
		t.Fatal("failed to parse tag A")
	}
	if !parsedA.MatchesScope(tagA.ScopePrefix()) {
		t.Error("tag A should match its own scope")
	}
	if parsedA.MatchesScope(tagB.ScopePrefix()) {
		t.Error("tag A should NOT match repo B's scope")
	}
}

func TestIsSFCacheTag_RetentionOnlyMatchesThisRepo(t *testing.T) {
	// Simulate shared cache target: two repos, list all tags, retention should
	// only match tags for the scoped repo.
	repoA := "https://gitlab.com/org/service-a"
	repoB := "https://gitlab.com/org/service-b"

	tagA := BuildCacheTag("cache", repoA, "main")
	tagB := BuildCacheTag("cache", repoB, "main")
	scopeA := tagA.ScopePrefix()

	if !IsSFCacheTag(tagA.String(), scopeA) {
		t.Error("retention should match repo A's own tag")
	}
	if IsSFCacheTag(tagB.String(), scopeA) {
		t.Error("retention must NOT match repo B's tag when scoped to repo A")
	}
}
