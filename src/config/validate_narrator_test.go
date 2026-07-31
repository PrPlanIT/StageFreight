package config

import (
	"strings"
	"testing"
)

// A build-contents item must declare which build owns its inventory once more
// than one build is configured — ownership is explicit, never inferred from
// build-list position. Single-build configs keep working without it.
func TestValidateBuildContentsBuildOwnership(t *testing.T) {
	base := StencilDef{
		Type:    "contents",
		Section: "inventories.apk",
		Render:  "badges",
	}
	multi := map[string]bool{"docker": true, "binary": true}
	single := map[string]bool{"docker": true}

	cases := []struct {
		name      string
		mutate    func(StencilDef) StencilDef
		builds    map[string]bool
		wantErrIn string // "" means expect no errors
	}{
		{"multi/no-build is a config error", nil, multi, "requires build"},
		{"multi/explicit valid build is ok", func(c StencilDef) StencilDef { c.Build = "docker"; return c }, multi, ""},
		{"multi/unknown build is rejected", func(c StencilDef) StencilDef { c.Build = "nope"; return c }, multi, "not a configured build"},
		{"multi/explicit source sidesteps build", func(c StencilDef) StencilDef { c.Source = "m.json"; return c }, multi, ""},
		{"single/no-build still ok (backward compat)", nil, single, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := base
			if tc.mutate != nil {
				item = tc.mutate(item)
			}
			errs := validateStencilDef(item, "scribe.content[apk]", tc.builds)
			if tc.wantErrIn == "" {
				if len(errs) != 0 {
					t.Fatalf("want no errors, got %v", errs)
				}
				return
			}
			for _, e := range errs {
				if strings.Contains(e, tc.wantErrIn) {
					return
				}
			}
			t.Fatalf("want an error containing %q, got %v", tc.wantErrIn, errs)
		})
	}
}
