package config

import "testing"

func TestScribeApplyStoreDefaults(t *testing.T) {
	c := &Config{Stencils: OrderedStencils{
		{ID: "build"}, // inline badge, no output → default store
		{ID: "custom", Output: ".sf/badges/x.svg"}, // explicit output → preserved
		{ID: "sh", Render: "shield", Message: "x"}, // shield → not a file badge, unaffected
	}}
	c.ApplyStencilStoreDefaults()
	if c.Stencils[0].Output != ".stagefreight/scribe/build.svg" {
		t.Errorf("default store path = %q", c.Stencils[0].Output)
	}
	if c.Stencils[1].Output != ".sf/badges/x.svg" {
		t.Errorf("explicit output not preserved: %q", c.Stencils[1].Output)
	}
	if c.Stencils[2].Output != "" {
		t.Errorf("shield should get no output path: %q", c.Stencils[2].Output)
	}
}

func TestScribeStore_Custom(t *testing.T) {
	c := &Config{Scribe: ScribeConfig{Store: ".stagefreight/assets"}, Stencils: OrderedStencils{{ID: "build"}}}
	c.ApplyStencilStoreDefaults()
	if c.Stencils[0].Output != ".stagefreight/assets/build.svg" {
		t.Errorf("custom store path = %q", c.Stencils[0].Output)
	}
}
