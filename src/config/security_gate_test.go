package config

import (
	"strings"
	"testing"
)

// A security exception must carry an id + reason (no anonymous suppression), and a present
// expiry must be a real date. Validate aggregates errors, so we assert the specific ones.
func TestSecurityExceptionValidation(t *testing.T) {
	// Valid exception → no exception-related error.
	if _, err := Validate(&Config{Version: 1, Security: SecurityConfig{
		FailOn:     "high",
		Exceptions: []SecurityException{{ID: "CVE-2026-14456", Reason: "no QUIC listener", Expires: "2026-12-31"}},
	}}); err != nil && strings.Contains(err.Error(), "security.exceptions") {
		t.Errorf("valid exception should not error: %v", err)
	}

	// Missing reason + missing id + bad date → three specific errors.
	_, err := Validate(&Config{Version: 1, Security: SecurityConfig{
		Exceptions: []SecurityException{
			{ID: "CVE-1"},                              // no reason
			{Reason: "x"},                              // no id
			{ID: "CVE-2", Reason: "x", Expires: "soon"}, // bad date
		},
	}})
	if err == nil {
		t.Fatal("expected validation errors for malformed exceptions")
	}
	for _, want := range []string{
		"security.exceptions[0].reason",
		"security.exceptions[1].id",
		"security.exceptions[2].expires",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing error %q in: %v", want, err)
		}
	}
}

func TestEffectiveFailOn(t *testing.T) {
	// explicit fail_on wins and is lowercased
	if got := (SecurityConfig{FailOn: "High"}).EffectiveFailOn(); got != "high" {
		t.Errorf("fail_on = %q, want high", got)
	}
	// deprecated fail_on_critical alias → critical
	if got := (SecurityConfig{FailOnCritical: true}).EffectiveFailOn(); got != "critical" {
		t.Errorf("fail_on_critical alias = %q, want critical", got)
	}
	// default is off (preserves today's informational gate)
	if got := (SecurityConfig{}).EffectiveFailOn(); got != "off" {
		t.Errorf("default = %q, want off", got)
	}
	// explicit fail_on beats the deprecated bool
	if got := (SecurityConfig{FailOn: "medium", FailOnCritical: true}).EffectiveFailOn(); got != "medium" {
		t.Errorf("fail_on should win over the alias, got %q", got)
	}
}

func TestUnreachablePolicy(t *testing.T) {
	if got := (SecurityConfig{}).UnreachablePolicy(); got != "pass" {
		t.Errorf("default = %q, want pass", got)
	}
	if got := (SecurityConfig{UnreachableVulns: "FAIL"}).UnreachablePolicy(); got != "fail" {
		t.Errorf("= %q, want fail", got)
	}
}
