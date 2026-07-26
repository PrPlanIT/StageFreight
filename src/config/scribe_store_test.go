package config

import "testing"

func TestScribeApplyStoreDefaults(t *testing.T) {
	s := &ScribeConfig{Content: OrderedContent{
		{ID: "build"},                                // inline badge, no output → default store
		{ID: "custom", Output: ".sf/badges/x.svg"},   // explicit output → preserved
		{ID: "sh", Render: "shield", Message: "x"},   // shield → not a file badge, unaffected
	}}
	s.ApplyStoreDefaults()
	if s.Content[0].Output != ".stagefreight/scribe/build.svg" {
		t.Errorf("default store path = %q", s.Content[0].Output)
	}
	if s.Content[1].Output != ".sf/badges/x.svg" {
		t.Errorf("explicit output not preserved: %q", s.Content[1].Output)
	}
	if s.Content[2].Output != "" {
		t.Errorf("shield should get no output path: %q", s.Content[2].Output)
	}
}

func TestScribeStore_Custom(t *testing.T) {
	s := &ScribeConfig{Store: ".stagefreight/assets", Content: OrderedContent{{ID: "build"}}}
	s.ApplyStoreDefaults()
	if s.Content[0].Output != ".stagefreight/assets/build.svg" {
		t.Errorf("custom store path = %q", s.Content[0].Output)
	}
}
