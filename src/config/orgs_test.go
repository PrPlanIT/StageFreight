package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrgs_Decode covers the orgs: id-map: key→ID stamping, maintainer + open aliases,
// document order, and strict rejection of an unknown field inside an entry.
func TestOrgs_Decode(t *testing.T) {
	data := `
HomeLabHD:
  maintainer: "HomeLabHD <ops@prplanit.com>"
  aliases: { handle: hlhd, lower: homelabhd }
PrPlanIT:
  maintainer: "PrPlanIT <ops@prplanit.com>"
`
	var orgs OrderedOrgs
	if err := yaml.Unmarshal([]byte(data), &orgs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("got %d orgs, want 2", len(orgs))
	}
	// Order preserved (id-maps keep document order).
	if orgs[0].ID != "HomeLabHD" || orgs[1].ID != "PrPlanIT" {
		t.Errorf("order = [%s %s], want [HomeLabHD PrPlanIT]", orgs[0].ID, orgs[1].ID)
	}

	h, ok := orgs.ByID("HomeLabHD")
	if !ok {
		t.Fatal("ByID(HomeLabHD) not found")
	}
	if h.Maintainer != "HomeLabHD <ops@prplanit.com>" {
		t.Errorf("maintainer = %q", h.Maintainer)
	}
	if h.Aliases["handle"] != "hlhd" || h.Aliases["lower"] != "homelabhd" {
		t.Errorf("aliases = %v", h.Aliases)
	}

	if _, ok := orgs.ByID("nope"); ok {
		t.Error("ByID(nope) should be false")
	}
}

// TestOrgs_StrictUnknownField verifies a typo'd key inside an org entry is rejected
// (KnownFields via the id-map strict node decode) rather than silently dropped.
func TestOrgs_StrictUnknownField(t *testing.T) {
	var orgs OrderedOrgs
	err := yaml.Unmarshal([]byte("HomeLabHD:\n  maintaner: x\n"), &orgs) // typo: maintaner
	if err == nil {
		t.Fatal("expected strict decode to reject unknown field 'maintaner'")
	}
}
