package config

import "testing"

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
