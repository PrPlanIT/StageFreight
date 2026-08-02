package config

import (
	"strings"
	"testing"
)

// TestNotificationEligibility covers the outcomes: dimension of the one when:
// grammar — alone, composed with ref conditions, OR over condition-sets, and the
// omitted-when: = always default.
func TestNotificationEligibility(t *testing.T) {
	elig := func(when WhenConditions, outcome, event, branch string) bool {
		return NotificationEligibility(when, outcome, event, branch, "", "", nil, nil).Eligible
	}

	// Omitted when: → always.
	if !elig(nil, "success", "push", "main") {
		t.Error("no when: should always match")
	}

	failureOnly := WhenConditions{{Outcomes: []string{"failure"}}}
	if !elig(failureOnly, "failure", "push", "main") || elig(failureOnly, "success", "push", "main") {
		t.Error("outcomes: [failure] should match failure only")
	}

	// Composition: "failures, but only on main" — branches resolve via re: inline.
	failMain := WhenConditions{{Outcomes: []string{"failure"}, Branches: []string{"re:^main$"}}}
	if !elig(failMain, "failure", "push", "main") {
		t.Error("failure on main should match")
	}
	if elig(failMain, "failure", "push", "feature") {
		t.Error("failure on a feature branch should not match")
	}

	// OR over condition-sets: failure anywhere, or success on main.
	either := WhenConditions{
		{Outcomes: []string{"failure"}},
		{Outcomes: []string{"success"}, Branches: []string{"re:^main$"}},
	}
	if !elig(either, "failure", "push", "feature") || !elig(either, "success", "push", "main") {
		t.Error("OR-list should match either set")
	}
	if elig(either, "success", "push", "feature") {
		t.Error("success off main should not match either set")
	}

	// unknown outcome matches only an empty outcomes:.
	if elig(failureOnly, "unknown", "push", "main") {
		t.Error("unknown outcome must not match a constrained outcomes:")
	}
}

// TestValidate_NotificationWhen covers the schema rules: outcome values are
// validated, and outcomes: on a publish target is rejected.
func TestValidate_NotificationWhen(t *testing.T) {
	bad := &Config{Version: 1, Notifications: OrderedNotifications{{
		ID: "x", Provider: "ntfy", URL: "https://ntfy.sh/t",
		When: WhenConditions{{Outcomes: []string{"broke"}}},
	}}}
	if _, err := Validate(bad); err == nil || !strings.Contains(err.Error(), "unknown outcome") {
		t.Errorf("bad outcome value should fail validation; got %v", err)
	}

	good := &Config{Version: 1, Notifications: OrderedNotifications{{
		ID: "x", Provider: "ntfy", URL: "https://ntfy.sh/t",
		When: WhenConditions{{Outcomes: []string{"failure", "warning"}}},
	}}}
	if _, err := Validate(good); err != nil && strings.Contains(err.Error(), "outcome") {
		t.Errorf("valid outcomes should pass; got %v", err)
	}

	pub := &Config{Version: 1, Targets: OrderedTargets{{
		ID: "t", Kind: "registry", Registry: StringOrList{"r"},
		When: WhenConditions{{Outcomes: []string{"failure"}}},
	}}}
	if _, err := Validate(pub); err == nil || !strings.Contains(err.Error(), "not valid on publish targets") {
		t.Errorf("outcomes: on a publish target should fail validation; got %v", err)
	}
}

// TestValidate_LLM covers the llms: library and the determinism boundary: llm
// stencils need a body and a known backend; only ollama is implemented; and a
// scribe file region referencing AI output — even through a text embed — fails.
func TestValidate_LLM(t *testing.T) {
	fails := func(cfg *Config, want string) {
		t.Helper()
		if _, err := Validate(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q; got %v", want, err)
		}
	}

	fails(&Config{Version: 1, Stencils: OrderedStencils{{ID: "t", Type: "llm", Body: "x", LLM: "nope"}}},
		`llm "nope" not found`)
	fails(&Config{Version: 1, Stencils: OrderedStencils{{ID: "t", Type: "llm", LLM: "l"}},
		LLMs: OrderedLLMs{{ID: "l", Provider: "ollama", URL: "http://x", Model: "m"}}},
		"requires body")
	fails(&Config{Version: 1, LLMs: OrderedLLMs{{ID: "l", Provider: "anthropic", Model: "m"}}},
		"not yet implemented")
	fails(&Config{Version: 1, LLMs: OrderedLLMs{{ID: "l", Provider: "ollama", Model: "m"}}},
		"requires url")

	// Dispatch-only: a file region embedding AI output transitively is rejected.
	tainted := &Config{Version: 1,
		LLMs: OrderedLLMs{{ID: "l", Provider: "ollama", URL: "http://x", Model: "m"}},
		Stencils: OrderedStencils{
			{ID: "fact", Type: "llm", LLM: "l", Body: "fun fact?"},
			{ID: "wrap", Type: "text", Body: "today: {fact}"},
		},
		Scribe: ScribeConfig{Files: OrderedFiles{{ID: "r", File: "README.md", Items: []string{"wrap"}}}},
	}
	fails(tainted, "dispatch-only")
}
