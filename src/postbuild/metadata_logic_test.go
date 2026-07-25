package postbuild

import (
	"strings"
	"testing"
)

func TestRegistryFieldsUsed(t *testing.T) {
	cases := []struct{ provider, want string }{
		{"dockerhub", "description,readme"}, // two distinct fields
		{"harbor", "readme"},                // single markdown field → readme wins
		{"quay", "readme"},
		{"jfrog", "description"}, // config description only; no readme API
		{"ghcr", ""},             // no description API
	}
	for _, c := range cases {
		if got := strings.Join(registryFieldsUsed(c.provider, "short", "full"), ","); got != c.want {
			t.Errorf("registryFieldsUsed(%s) = %q, want %q", c.provider, got, c.want)
		}
	}
	// Single-field fallback: harbor with no readme falls back to the short description.
	if got := strings.Join(registryFieldsUsed("harbor", "short", ""), ","); got != "description" {
		t.Errorf("harbor no-readme fallback = %q, want description", got)
	}
}

func TestFitDescription(t *testing.T) {
	variants := []string{
		"A long tagline that is deliberately way over one hundred characters so it cannot fit a Docker Hub short-description cap at all, no way",
		"A medium tagline comfortably under one hundred chars",
		"short",
	}
	// Docker Hub cap 100 → the medium (longest that fits).
	if got, ok := fitDescription(variants, 100); !ok || got != "A medium tagline comfortably under one hundred chars" {
		t.Fatalf("cap 100: got %q ok=%v", got, ok)
	}
	// No cap → the fullest variant.
	if got, _ := fitDescription(variants, 0); len([]rune(got)) < 100 {
		t.Fatalf("no cap should pick the fullest, got %q", got)
	}
	// Nothing fits → (,"",false) so the caller warns+skips.
	if _, ok := fitDescription([]string{"toolongtoolong"}, 3); ok {
		t.Fatal("expected no fit for cap 3")
	}
	// No variants → no fit.
	if _, ok := fitDescription(nil, 100); ok {
		t.Fatal("expected no fit for empty variants")
	}
}

func TestNormalizeTopics(t *testing.T) {
	out, warnings := normalizeTopics([]string{"Machine Learning", "gitops", "C++"})
	if len(out) != 3 || out[0] != "machine-learning" || out[1] != "gitops" || out[2] != "c" {
		t.Fatalf("normalized wrong: %v", out)
	}
	// "Machine Learning" and "C++" both transformed → 2 warnings; "gitops" unchanged.
	if len(warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", warnings)
	}
	if out, w := normalizeTopics(nil); out != nil || w != nil {
		t.Fatalf("nil topics should produce nil, nil; got %v %v", out, w)
	}
}
