package facts

import (
	"strings"
	"testing"

	"github.com/PrPlanIT/StageFreight/src/config"
	"github.com/PrPlanIT/StageFreight/src/gitver"
)

func identityTestConfig() *config.Config {
	return &config.Config{
		Orgs: config.OrderedOrgs{
			{ID: "HomeLabHD", Maintainer: "HomeLabHD <ops@prplanit.com>", Aliases: map[string]string{"handle": "hlhd"}},
		},
		Metadata: config.MetadataConfig{
			Org:         "HomeLabHD",
			Title:       "ARK Server",
			License:     "MIT",
			Description: config.Scoped[config.StringOrList]{Default: config.StringOrList{"short", "long"}},
			Labels:      map[string]string{"funding": "https://example.com/sponsor"},
		},
	}
}

// TestIdentityResolver covers the {org.*}, {orgs.<id>.*}, and {metadata.*} families,
// including alias resolution, the description shortest-tier, labels, and that a typo'd
// token is left literal.
func TestIdentityResolver(t *testing.T) {
	r := IdentityResolver()
	c := &Context{Config: identityTestConfig()}

	cases := []struct{ in, want string }{
		{"{org} {org.lower} {org.handle}", "HomeLabHD homelabhd hlhd"},
		{"{org.maintainer}", "HomeLabHD <ops@prplanit.com>"},
		{"{metadata.title} / {metadata.license} / {metadata.description}", "ARK Server / MIT / short"},
		{"{orgs.HomeLabHD.handle}", "hlhd"},
		{"{metadata.labels.funding}", "https://example.com/sponsor"},
		{"{metadata.titel}", "{metadata.titel}"}, // typo → left literal (visible)
		{"{org.nonalias}", "{org.nonalias}"},     // undeclared alias → literal
	}
	for _, tc := range cases {
		got := r.Resolve([]string{tc.in}, c)[0]
		if got != tc.want {
			t.Errorf("Resolve(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIdentityResolver_NoConfig / unknown org: absent config or an unresolvable org ref
// leaves {org.*} literal (aliases are open, so they can't be blanked), while the closed
// {metadata.*} fields still resolve to "".
func TestIdentityResolver_UnknownOrg(t *testing.T) {
	r := IdentityResolver()
	cfg := &config.Config{Metadata: config.MetadataConfig{Org: "Ghost", License: "MIT"}}
	got := r.Resolve([]string{"{org.handle} {metadata.license}"}, &Context{Config: cfg})[0]
	if got != "{org.handle} MIT" {
		t.Errorf("got %q, want %q", got, "{org.handle} MIT")
	}
	// Nil config → passthrough.
	if got := r.Resolve([]string{"{org}"}, &Context{})[0]; got != "{org}" {
		t.Errorf("nil config: got %q, want literal", got)
	}
}

// TestScribeRegistry_IdentityAndGitver verifies identity + gitver compose through the
// registry: a metadata value can carry a gitver token that the leaf pass then resolves.
func TestScribeRegistry_IdentityAndGitver(t *testing.T) {
	cfg := identityTestConfig()
	got := ScribeRegistry().ResolveOne("{metadata.title} {version}", &Context{
		Config:  cfg,
		Version: &gitver.VersionInfo{Version: "9.9.9"},
	})
	if !strings.Contains(got, "ARK Server") || !strings.Contains(got, "9.9.9") {
		t.Errorf("got %q, want both identity and gitver resolved", got)
	}
}
