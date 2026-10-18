package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResolveCron_Validates(t *testing.T) {
	ok := []string{"0 4 * * *", "H H * * 1", "30 */6 * * 1-5", "5 0 1,15 * *", "59 23 31 12 6"}
	for _, c := range ok {
		if _, err := ResolveCron(c, "seed", "id"); err != nil {
			t.Errorf("%q rejected: %v", c, err)
		}
	}
	bad := map[string]string{
		"* * * * *":    "at most hourly",
		"*/5 * * * *":  "at most hourly",
		"0,30 * * * *": "at most hourly",
		"0 4 * *":      "want 5 fields",
		"0 24 * * *":   "hour",
		"0 4 H * *":    "only in the minute and hour",
		"0 4 * * MON":  "day-of-week",
		"0 4 * * 7":    "day-of-week",
		"0 4 * */0 *":  "positive number",
		"0 4 * * 1-":   "day-of-week",
		"@daily":       "want 5 fields",
	}
	for c, want := range bad {
		if _, err := ResolveCron(c, "seed", "id"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to mention %q", c, err, want)
		}
	}
}

func TestResolveCron_HashIsStableAndSpread(t *testing.T) {
	a1, _ := ResolveCron("H H * * *", "https://gitlab.example.com/org/a", "nightly")
	a2, _ := ResolveCron("H H * * *", "https://gitlab.example.com/org/a", "nightly")
	if a1 != a2 {
		t.Fatalf("H must be stable across renders: %q vs %q", a1, a2)
	}
	seen := map[string]bool{}
	for _, repo := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		c, err := ResolveCron("H 4 * * *", "https://gitlab.example.com/org/"+repo, "nightly")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(c, " 4 * * *") {
			t.Fatalf("only H is replaced: %q", c)
		}
		seen[c] = true
	}
	if len(seen) < 4 {
		t.Fatalf("H should spread a fleet across minutes, got %d distinct of 8", len(seen))
	}
}

func TestSchedules_DecodeGroupAndValidate(t *testing.T) {
	var cfg Config
	src := `
schedules:
  nightly: { cron: '17 4 * * *', reason: upstream watch }
  deps:    { cron: '17 4 * * *' }
  weekly:  { cron: '42 6 * * 1' }
`
	if err := yaml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatal(err)
	}
	got := ResolvedSchedules(&cfg)
	if len(got) != 2 || strings.Join(got[0].IDs, ",") != "nightly,deps" || got[1].Cron != "42 6 * * 1" {
		t.Fatalf("resolved = %+v, want two cadences, the first shared", got)
	}
	if errs := validateSchedules(&cfg); len(errs) != 0 {
		t.Fatalf("valid schedules rejected: %v", errs)
	}
	cfg.Schedules = append(cfg.Schedules, ScheduleConfig{ID: "bad id", Cron: "* * * * *"}, ScheduleConfig{ID: "empty"})
	errs := strings.Join(validateSchedules(&cfg), "\n")
	for _, want := range []string{"schedules.bad id: the id", "schedules.bad id.cron", "schedules.empty.cron: required"} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors %q, want %q", errs, want)
		}
	}
}

func TestWhenSchedules_UnknownIDRejected(t *testing.T) {
	when := WhenConditions{{Schedules: []string{"nightly", "hourly"}}}
	errs := strings.Join(validateWhen(when, "notifications.n", nil, nil, OrderedSchedules{{ID: "nightly"}}), "\n")
	if !strings.Contains(errs, `unknown schedule "hourly"`) || strings.Contains(errs, `"nightly"`) {
		t.Fatalf("errors = %q", errs)
	}
}

func TestWhenSchedules_Eligibility(t *testing.T) {
	defer func(f func() []string) { ciSchedules = f }(ciSchedules)
	target := TargetConfig{When: WhenConditions{{Schedules: []string{"weekly"}}}}

	ciSchedules = func() []string { return []string{"nightly", "deps"} }
	if r := TargetEligibility(target, "schedule", "main", "", "", nil, nil); r.Eligible || !strings.Contains(r.Reason, "schedules:[weekly]") {
		t.Fatalf("a run fired by other schedules must not match: %+v", r)
	}
	ciSchedules = func() []string { return []string{"weekly"} }
	if r := TargetEligibility(target, "schedule", "main", "", "", nil, nil); !r.Eligible {
		t.Fatalf("the named schedule must match: %+v", r)
	}
	ciSchedules = func() []string { return nil }
	if TargetEligibility(target, "push", "main", "", "", nil, nil).Eligible {
		t.Fatal("a push is not a scheduled run")
	}
}

func TestCISchedules(t *testing.T) {
	t.Setenv("SF_CI_SCHEDULE", " nightly, deps ,")
	if got := strings.Join(CISchedules(), ","); got != "nightly,deps" {
		t.Fatalf("CISchedules = %q", got)
	}
	t.Setenv("SF_CI_SCHEDULE", "")
	if CISchedules() != nil {
		t.Fatal("no schedule outside a scheduled run")
	}
}
