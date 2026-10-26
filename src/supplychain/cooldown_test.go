package supplychain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const day = 24 * time.Hour

func TestParseAge(t *testing.T) {
	cases := map[string]time.Duration{
		"7d": 7 * day, "2w": 14 * day, "72h": 72 * time.Hour, "1.5d": 36 * time.Hour, "": 0, "0": 0,
	}
	for in, want := range cases {
		if got, err := ParseAge(in); err != nil || got != want {
			t.Errorf("ParseAge(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"soon", "-3d", "3x"} {
		if _, err := ParseAge(bad); err == nil {
			t.Errorf("ParseAge(%q) must fail", bad)
		}
	}
}

// TestCooldown_Forms: the scalar applies to every size; the map sets sizes, with
// default: covering those left out.
func TestCooldown_Forms(t *testing.T) {
	cases := map[string]Cooldown{
		`3d`:                                 {Major: 3 * day, Minor: 3 * day, Patch: 3 * day},
		`0`:                                  {},
		`{major: 14d, minor: 7d, patch: 3d}`: {Major: 14 * day, Minor: 7 * day, Patch: 3 * day},
		`{default: 3d, major: 14d}`:          {Major: 14 * day, Minor: 3 * day, Patch: 3 * day},
		`{major: 30d}`:                       {Major: 30 * day},
		`{default: 2d, patch: 0}`:            {Major: 2 * day, Minor: 2 * day},
	}
	for in, want := range cases {
		var c Cooldown
		if err := yaml.Unmarshal([]byte(in), &c); err != nil || c != want {
			t.Errorf("%s → %+v, %v; want %+v", in, c, err, want)
		}
	}
	for in, msg := range map[string]string{
		`soon`:          "not a duration",
		`{weekly: 3d}`:  "min_release_age.weekly: unknown key",
		`{major: soon}`: "min_release_age.major",
		`[3d]`:          "must be a duration",
	} {
		var c Cooldown
		if err := yaml.Unmarshal([]byte(in), &c); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s must fail mentioning %q, got %v", in, msg, err)
		}
	}
}

func TestCooldown_Window(t *testing.T) {
	c := Cooldown{Major: 14 * day, Minor: 7 * day, Patch: 3 * day}
	for size, want := range map[string]time.Duration{"major": 14 * day, "minor": 7 * day, "patch": 3 * day, "": 14 * day, "tag": 14 * day} {
		if got := c.Window(size); got != want {
			t.Errorf("Window(%q) = %v, want %v", size, got, want)
		}
	}
}

// TestCooldown_JSONRoundTrip: the value config normalization puts in the freshness
// options survives the options' JSON round-trip.
func TestCooldown_JSONRoundTrip(t *testing.T) {
	in := Cooldown{Major: 14 * day, Minor: 7 * day, Patch: 36 * time.Hour}
	b, err := json.Marshal(map[string]any{"min_release_age": in})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		C Cooldown `json:"min_release_age"`
	}
	if err := json.Unmarshal(b, &out); err != nil || out.C != in {
		t.Errorf("round trip = %+v, %v; want %+v", out.C, err, in)
	}
	if err := json.Unmarshal([]byte(`{"min_release_age":"3d"}`), &out); err != nil || out.C.Patch != 3*day {
		t.Errorf("a scalar option must parse, got %+v, %v", out.C, err)
	}
}
