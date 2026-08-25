package postbuild

import "testing"

// parseInventoryToken must split cluster / field, anchoring the field on the right
// so a cluster name with dots still parses.
func TestParseInventoryToken(t *testing.T) {
	cases := []struct{ inner, cluster, field string }{
		{"dungeon.count", "dungeon", "count"},
		{"ad.arbitorium.count", "ad.arbitorium", "count"},
		{"dungeon.bogus", "", ""},  // unknown field
		{"dungeon", "", ""},        // no field
		{"count", "", ""},          // no cluster
	}
	for _, c := range cases {
		cl, f := parseInventoryToken(c.inner)
		if cl != c.cluster || f != c.field {
			t.Errorf("parseInventoryToken(%q) = (%q,%q), want (%q,%q)", c.inner, cl, f, c.cluster, c.field)
		}
	}
}

func TestExtractInventoryClusters(t *testing.T) {
	values := []string{
		"apps: {inventory.dungeon.count}",
		"nothing here",
		"{inventory.dungeon.count} and {inventory.homelab.count}",
		"{inventory.malformed", // no close brace → ignored
	}
	got := extractInventoryClusters(values)
	for _, want := range []string{"dungeon", "homelab"} {
		if !got[want] {
			t.Errorf("extractInventoryClusters missing %q (got %v)", want, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("extractInventoryClusters len = %d, want 2 (%v)", len(got), got)
	}
}

// resolveInventoryTokens substitutes known counts and leaves unknown clusters literal
// (so the badge layer renders "n/a").
func TestResolveInventoryTokens(t *testing.T) {
	counts := map[string]int{"dungeon": 110}
	cases := []struct{ in, want string }{
		{"{inventory.dungeon.count}", "110"},
		{"apps: {inventory.dungeon.count} live", "apps: 110 live"},
		{"{inventory.unknown.count}", "{inventory.unknown.count}"},
		{"no token", "no token"},
	}
	for _, c := range cases {
		if got := resolveInventoryTokens(c.in, counts); got != c.want {
			t.Errorf("resolveInventoryTokens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
