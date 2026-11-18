package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDeepMerge_NullRemovesKey(t *testing.T) {
	var base, over yaml.Node
	if err := yaml.Unmarshal([]byte("a: 1\nb: {c: 2, d: 3}\n"), &base); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal([]byte("a: null\nb: {d: ~}\n"), &over); err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(DeepMerge(base.Content[0], over.Content[0]))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "b:\n    c: 2\n" {
		t.Errorf("merged:\n%s", got)
	}
}
