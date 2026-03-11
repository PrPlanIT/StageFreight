package props

import (
	"strings"
	"testing"
)

func TestAllDefinitions(t *testing.T) {
	defs := All()
	if len(defs) == 0 {
		t.Fatal("no definitions registered")
	}

	for _, def := range defs {
		t.Run(def.ID, func(t *testing.T) {
			// 1. DefaultAlt is non-empty
			if def.DefaultAlt == "" {
				t.Errorf("DefaultAlt is empty")
			}

			// 2. Format is non-empty
			if def.Format == "" {
				t.Errorf("Format is empty")
			}

			// 3. Category is non-empty
			if def.Category == "" {
				t.Errorf("Category is empty")
			}

			// 4. Resolver is not nil
			if def.Resolver == nil {
				t.Fatalf("Resolver is nil")
			}

			// 5. Schema().Params declares all required params — verify example covers them
			schema := def.Resolver.Schema()
			for _, p := range schema.Params {
				if p.Required {
					if _, ok := schema.Example[p.Name]; !ok {
						t.Errorf("Schema().Example missing required param %q", p.Name)
					}
				}
			}

			// 6. Schema().Example satisfies ValidateParams()
			if err := ValidateParams(def, schema.Example); err != nil {
				t.Errorf("Schema().Example fails ValidateParams: %v", err)
			}

			// 7. ResolveDefinition succeeds with example params
			resolved, err := ResolveDefinition(def, schema.Example, RenderOptions{})
			if err != nil {
				t.Fatalf("ResolveDefinition failed: %v", err)
			}

			// 8. resolved ResolvedProp.ImageURL is non-empty
			if resolved.ImageURL == "" {
				t.Errorf("resolved ImageURL is empty")
			}

			// 9. resolved ResolvedProp.Alt is non-empty
			if resolved.Alt == "" {
				t.Errorf("resolved Alt is empty")
			}

			// 10. no unresolved {placeholder} tokens in ImageURL or LinkURL
			if containsPlaceholder(resolved.ImageURL) {
				t.Errorf("ImageURL contains unresolved placeholder: %s", resolved.ImageURL)
			}
			if containsPlaceholder(resolved.LinkURL) {
				t.Errorf("LinkURL contains unresolved placeholder: %s", resolved.LinkURL)
			}

			// 11. Description is non-empty
			if def.Description == "" {
				t.Errorf("Description is empty")
			}

			// 12. If provider is shields or native, LinkURL should generally be non-empty
			//     (static types like conventional-commits may have links, slack may not need one
			//      but most should). Skip visitor-count which intentionally has no link.
			if def.Provider != ProviderStatic && resolved.LinkURL == "" {
				// visitor-count is native but intentionally link-less
				if def.ID != "visitor-count" {
					t.Errorf("non-static definition has empty LinkURL")
				}
			}

			// 13. FormatMarkdown produces non-empty output
			md := FormatMarkdown(resolved, VariantClassic)
			if md == "" {
				t.Errorf("FormatMarkdown returned empty string")
			}
		})
	}
}

func TestAllDefinitionsCount(t *testing.T) {
	defs := All()
	if len(defs) < 37 {
		t.Errorf("expected at least 37 definitions, got %d", len(defs))
	}
}

func TestCategories(t *testing.T) {
	cats := Categories()
	if len(cats) == 0 {
		t.Fatal("no categories returned")
	}
	// Verify sorted
	for i := 1; i < len(cats); i++ {
		if cats[i].Name < cats[i-1].Name {
			t.Errorf("categories not sorted: %s < %s", cats[i].Name, cats[i-1].Name)
		}
	}
}

func TestListByCategory(t *testing.T) {
	dockerTypes := List("docker")
	if len(dockerTypes) == 0 {
		t.Fatal("no docker category types")
	}
	for _, d := range dockerTypes {
		if d.Category != "docker" {
			t.Errorf("expected category docker, got %s", d.Category)
		}
	}
}

func TestGetDefinition(t *testing.T) {
	def, ok := Get("docker-pulls")
	if !ok {
		t.Fatal("docker-pulls not found")
	}
	if def.ID != "docker-pulls" {
		t.Errorf("expected docker-pulls, got %s", def.ID)
	}

	_, ok = Get("nonexistent-type")
	if ok {
		t.Error("expected nonexistent-type to not be found")
	}
}

func TestValidateParamsUnknown(t *testing.T) {
	def, _ := Get("docker-pulls")
	err := ValidateParams(def, map[string]string{
		"image":   "prplanit/stagefreight",
		"unknown": "value",
	})
	if err == nil {
		t.Fatal("expected error for unknown param")
	}
	if !strings.Contains(err.Error(), "unknown parameter") {
		t.Errorf("expected 'unknown parameter' in error, got: %v", err)
	}
}

func TestValidateParamsMissing(t *testing.T) {
	def, _ := Get("docker-pulls")
	err := ValidateParams(def, map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing required param")
	}
	if !strings.Contains(err.Error(), "required parameter missing") {
		t.Errorf("expected 'required parameter missing' in error, got: %v", err)
	}
}

func TestRenderOptionsOverrides(t *testing.T) {
	def, _ := Get("docker-pulls")
	resolved, err := ResolveDefinition(def, map[string]string{"image": "prplanit/stagefreight"}, RenderOptions{
		Label: "Custom Label",
		Link:  "https://custom.example.com",
	})
	if err != nil {
		t.Fatalf("ResolveDefinition failed: %v", err)
	}
	if resolved.Alt != "Custom Label" {
		t.Errorf("expected Alt 'Custom Label', got %q", resolved.Alt)
	}
	if resolved.LinkURL != "https://custom.example.com" {
		t.Errorf("expected LinkURL override, got %q", resolved.LinkURL)
	}
}

func TestVariantValidation(t *testing.T) {
	// Valid variants
	if err := ValidateVariant(""); err != nil {
		t.Errorf("empty variant should be valid: %v", err)
	}
	if err := ValidateVariant(VariantClassic); err != nil {
		t.Errorf("classic variant should be valid: %v", err)
	}

	// Invalid variant
	if err := ValidateVariant("neon"); err == nil {
		t.Error("unknown variant should produce error")
	}

	// Invalid variant through ResolveDefinition
	def, _ := Get("docker-pulls")
	_, err := ResolveDefinition(def, map[string]string{"image": "prplanit/stagefreight"}, RenderOptions{
		Variant: "neon",
	})
	if err == nil {
		t.Fatal("expected error for unknown variant")
	}
	if !strings.Contains(err.Error(), "unknown variant") {
		t.Errorf("expected 'unknown variant' in error, got: %v", err)
	}
}

func TestFormatMarkdown(t *testing.T) {
	tests := []struct {
		name string
		prop ResolvedProp
		want string
	}{
		{
			name: "with link",
			prop: ResolvedProp{ImageURL: "https://img.shields.io/docker/pulls/prplanit/stagefreight", LinkURL: "https://hub.docker.com/r/prplanit/stagefreight", Alt: "Docker Pulls"},
			want: "[![Docker Pulls](https://img.shields.io/docker/pulls/prplanit/stagefreight)](https://hub.docker.com/r/prplanit/stagefreight)",
		},
		{
			name: "without link",
			prop: ResolvedProp{ImageURL: "https://img.shields.io/docker/pulls/prplanit/stagefreight", Alt: "Docker Pulls"},
			want: "![Docker Pulls](https://img.shields.io/docker/pulls/prplanit/stagefreight)",
		},
		{
			name: "empty image",
			prop: ResolvedProp{},
			want: "",
		},
		{
			name: "empty alt falls back to badge",
			prop: ResolvedProp{ImageURL: "https://example.com/badge.svg"},
			want: "![badge](https://example.com/badge.svg)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatMarkdown(tt.prop, VariantClassic)
			if got != tt.want {
				t.Errorf("FormatMarkdown() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShieldsBadgeEscaping(t *testing.T) {
	// website resolver should URL-encode labels with special chars
	def, ok := Get("website")
	if !ok {
		t.Fatal("website not found")
	}
	resolved, err := ResolveDefinition(def, map[string]string{
		"url":   "https://example.com",
		"label": "My Site",
	}, RenderOptions{})
	if err != nil {
		t.Fatalf("ResolveDefinition failed: %v", err)
	}
	// "My Site" should be encoded, not raw
	if strings.Contains(resolved.ImageURL, "My Site") {
		t.Errorf("ImageURL contains unencoded label: %s", resolved.ImageURL)
	}
}

func TestStyleFlowsThroughRenderOptions(t *testing.T) {
	// style is NOT a param — putting it in params should error
	def, _ := Get("docker-pulls")
	err := ValidateParams(def, map[string]string{
		"image": "prplanit/stagefreight",
		"style": "flat-square",
	})
	if err == nil {
		t.Fatal("style in params should be an unknown parameter error")
	}

	// style through RenderOptions should produce query param
	resolved, err := ResolveDefinition(def, map[string]string{"image": "prplanit/stagefreight"}, RenderOptions{
		Style: "flat-square",
	})
	if err != nil {
		t.Fatalf("ResolveDefinition with style option failed: %v", err)
	}
	if !strings.Contains(resolved.ImageURL, "style=flat-square") {
		t.Errorf("expected style=flat-square in URL, got: %s", resolved.ImageURL)
	}
}
