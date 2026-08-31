package facts

import (
	"testing"
	"time"

	"github.com/PrPlanIT/StageFreight/src/gitver"
)

// parseRegistryToken must split id / kind / tag / field correctly — including dotted tag
// names (v1.18.4) where the field is anchored from the right, and the size:raw field which
// carries a colon, not a dot.
func TestParseRegistryToken(t *testing.T) {
	cases := []struct {
		inner              string
		id, kind, tag, fld string
	}{
		{"dockerhub.pulls", "dockerhub", "repo", "", "pulls"},
		{"dockerhub.pulls:raw", "dockerhub", "repo", "", "pulls:raw"},
		{"dockerhub.stars", "dockerhub", "repo", "", "stars"},
		{"ghcr.tag.latest-dev.updated", "ghcr", "tag", "latest-dev", "updated"},
		{"ghcr.tag.latest-dev.size", "ghcr", "tag", "latest-dev", "size"},
		{"ghcr.tag.latest-dev.size:raw", "ghcr", "tag", "latest-dev", "size:raw"},
		{"ghcr.tag.v1.18.4.size", "ghcr", "tag", "v1.18.4", "size"},   // dotted tag
		{"ghcr.tag.v1.2.3.digest", "ghcr", "tag", "v1.2.3", "digest"}, // dotted tag
		{"ghcr.tag.latest.bogus", "", "", "", ""},                     // unknown field → rejected
		{"noDotHere", "", "", "", ""},                                 // no id boundary
	}
	for _, c := range cases {
		t.Run(c.inner, func(t *testing.T) {
			id, kind, tag, fld := parseRegistryToken(c.inner)
			if id != c.id || kind != c.kind || tag != c.tag || fld != c.fld {
				t.Errorf("parseRegistryToken(%q) = (%q,%q,%q,%q), want (%q,%q,%q,%q)",
					c.inner, id, kind, tag, fld, c.id, c.kind, c.tag, c.fld)
			}
		})
	}
}

// extractRegistryRefs must collect, per id, the tag names referenced and whether a
// repo-level field was seen — so each registry is fetched once for exactly what's asked.
func TestExtractRegistryRefs(t *testing.T) {
	values := []string{
		"pulls: {registry.dockerhub.pulls}",
		"size: {registry.ghcr.tag.latest-dev.size} / {registry.ghcr.tag.v1.18.4.updated}",
		"no tokens here",
	}
	refs := extractRegistryRefs(values)
	if len(refs) != 2 {
		t.Fatalf("want 2 registries referenced, got %d (%v)", len(refs), keys(refs))
	}
	if !refs["dockerhub"].repoInfo {
		t.Error("dockerhub should be flagged for repo-level info (pulls)")
	}
	if !refs["ghcr"].tags["latest-dev"] || !refs["ghcr"].tags["v1.18.4"] {
		t.Errorf("ghcr tags not collected: %v", refs["ghcr"].tags)
	}
}

// resolveRegistryTokens must substitute fetched values, format per field, and leave unknown
// ids / missing tags as empty (which the badge layer renders as n/a) — never a stray token.
func TestResolveRegistryTokens(t *testing.T) {
	infos := map[string]registryInfo{
		"dockerhub": {pulls: 1247, tags: map[string]gitver.TagInfo{}},
		"ghcr": {tags: map[string]gitver.TagInfo{
			"latest-dev": {Size: 75_890_432, LastUpdated: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)},
		}},
	}
	cases := []struct{ in, want string }{
		{"{registry.dockerhub.pulls}", "1.2k"},
		{"{registry.dockerhub.pulls:raw}", "1247"},
		{"{registry.ghcr.tag.latest-dev.size}", "72.4 MB"},
		{"{registry.ghcr.tag.latest-dev.updated}", "2026-08-24"},
		{"v{registry.ghcr.tag.latest-dev.updated}!", "v2026-08-24!"},
		{"{registry.ghcr.tag.missing.size}", ""}, // tag not fetched → empty
		{"{registry.unknown.pulls}", ""},         // unknown id → empty
		{"plain text", "plain text"},             // untouched
	}
	for _, c := range cases {
		if got := resolveRegistryTokens(c.in, infos); got != c.want {
			t.Errorf("resolveRegistryTokens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func keys(m map[string]*registryRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
