package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ScheduleConfig is one declared schedule: a cadence on which StageFreight runs by itself.
// The forge fires it (StageFreight materializes it into each forge's own scheduler); the run
// knows which schedule fired, and when.schedules selects work by id.
type ScheduleConfig struct {
	ID string `yaml:"-"`

	// Cron is a 5-field cron expression in UTC: minute hour day-of-month month day-of-week.
	// The minute is a single value, so a schedule fires at most hourly — the floor every forge
	// honours. H in the minute or hour field is a value hashed from the repo and schedule id,
	// so a fleet sharing one cadence does not fire at the same instant.
	Cron string `yaml:"cron"`

	// Reason says what the schedule is for; shown wherever the schedule is materialized.
	Reason string `yaml:"reason,omitempty"`

	// Build decides whether the scheduled run builds and publishes what it runs on:
	//   changes (default): no. The run watches upstreams, bumps, syncs; whatever it commits
	//                      starts its own pipeline, which builds and publishes. A night with
	//                      nothing new publishes nothing — no fresh digests to chase.
	//   always:            yes — rebuild and republish the current commit (a base-image
	//                      refresh). Publish targets can narrow it with when.schedules.
	Build string `yaml:"build,omitempty"`
}

// Schedule build modes.
const (
	ScheduleBuildChanges = "changes"
	ScheduleBuildAlways  = "always"
)

// ScheduledRunBuilds reports whether this scheduled run builds and publishes: when any
// schedule that fired it says build: always, or when the run cannot name a declared schedule
// (a schedule StageFreight did not declare keeps the push-equivalent behaviour). Always true
// outside a scheduled run.
func ScheduledRunBuilds(cfg *Config) bool {
	if CIEvent() != "schedule" {
		return true
	}
	fired := CISchedules()
	if len(fired) == 0 {
		return true
	}
	for _, id := range fired {
		s, ok := findSchedule(cfg.Schedules, id)
		if !ok || s.Build == ScheduleBuildAlways {
			return true
		}
	}
	return false
}

func findSchedule(all OrderedSchedules, id string) (ScheduleConfig, bool) {
	for _, s := range all {
		if s.ID == id {
			return s, true
		}
	}
	return ScheduleConfig{}, false
}

// OrderedSchedules is the schedules: block — an id→schedule map (key becomes ScheduleConfig.ID).
type OrderedSchedules []ScheduleConfig

func (OrderedSchedules) isIDMap() {}

func (o *OrderedSchedules) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeIDMap(n, func(s *ScheduleConfig, id string) { s.ID = id })
	if err != nil {
		return fmt.Errorf("schedules: %w", err)
	}
	*o = v
	return nil
}

// cronFields bounds each cron field: name, minimum, maximum, and whether H may stand in it.
var cronFields = []struct {
	name     string
	min, max int
	hashable bool
}{
	{"minute", 0, 59, true},
	{"hour", 0, 23, true},
	{"day-of-month", 1, 31, false},
	{"month", 1, 12, false},
	{"day-of-week", 0, 6, false},
}

// ResolveCron validates a declared cron and replaces each H with its hashed value. seed
// identifies the repo; together with the schedule id it makes H stable across renders and
// distinct across repos.
func ResolveCron(cron, seed, id string) (string, error) {
	fields := strings.Fields(cron)
	if len(fields) != len(cronFields) {
		return "", fmt.Errorf("cron %q: want 5 fields (minute hour day-of-month month day-of-week), found %d", cron, len(fields))
	}
	h := fnv.New32a()
	h.Write([]byte(seed + "\x00" + id))
	sum := h.Sum32()
	out := make([]string, len(fields))
	for i, f := range fields {
		spec := cronFields[i]
		if f == "H" {
			if !spec.hashable {
				return "", fmt.Errorf("cron %q: H is allowed only in the minute and hour fields", cron)
			}
			if i == 0 {
				out[i] = strconv.Itoa(int(sum % 60))
			} else {
				out[i] = strconv.Itoa(int(sum / 60 % 24))
			}
			continue
		}
		if i == 0 {
			n, err := strconv.Atoi(f)
			if err != nil || n < spec.min || n > spec.max {
				return "", fmt.Errorf("cron %q: the minute must be a single value 0-59 or H, so the schedule fires at most hourly", cron)
			}
		} else if err := checkCronField(f, spec.min, spec.max); err != nil {
			return "", fmt.Errorf("cron %q: %s: %v", cron, spec.name, err)
		}
		out[i] = f
	}
	return strings.Join(out, " "), nil
}

// checkCronField accepts the portable cron grammar: *, n, a-b, with an optional /step, in
// comma-separated lists. Names (MON, JAN) and forge extensions are rejected so the same
// expression means the same thing on every forge.
func checkCronField(f string, min, max int) error {
	for _, part := range strings.Split(f, ",") {
		base, step, stepped := strings.Cut(part, "/")
		if stepped {
			if n, err := strconv.Atoi(step); err != nil || n < 1 {
				return fmt.Errorf("step %q must be a positive number", step)
			}
		}
		if base == "*" {
			continue
		}
		lo, hi, ranged := strings.Cut(base, "-")
		vals := []string{lo}
		if ranged {
			vals = append(vals, hi)
		}
		for _, v := range vals {
			n, err := strconv.Atoi(v)
			if err != nil || n < min || n > max {
				return fmt.Errorf("%q must be *, a number %d-%d, or a range of them", part, min, max)
			}
		}
	}
	return nil
}

// ScheduleSeed is the repo identity H is hashed from: the primary repo's URL, else its name.
func ScheduleSeed(cfg *Config) string {
	if u := PrimaryURL(cfg); u != "" {
		return u
	}
	return SlugFromPrimary(cfg)
}

// ResolvedSchedule is one distinct cadence to materialize: its resolved cron and every
// schedule id that shares it (a forge fires one run for them all).
type ResolvedSchedule struct {
	Cron string
	IDs  []string
}

// ResolvedSchedules resolves every declared schedule and groups ids by cron, in declaration
// order. Schedules that do not resolve are skipped; validation reports them.
func ResolvedSchedules(cfg *Config) []ResolvedSchedule {
	seed := ScheduleSeed(cfg)
	var out []ResolvedSchedule
	index := map[string]int{}
	for _, s := range cfg.Schedules {
		cron, err := ResolveCron(s.Cron, seed, s.ID)
		if err != nil {
			continue
		}
		if i, ok := index[cron]; ok {
			out[i].IDs = append(out[i].IDs, s.ID)
			continue
		}
		index[cron] = len(out)
		out = append(out, ResolvedSchedule{Cron: cron, IDs: []string{s.ID}})
	}
	return out
}

// CISchedules returns the ids of the schedules that fired this run ("" outside a scheduled
// run). Every forge's rendered CI (or its schedule variables) sets SF_CI_SCHEDULE to them.
func CISchedules() []string {
	var out []string
	for _, id := range strings.Split(os.Getenv("SF_CI_SCHEDULE"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// validateSchedules checks the schedules: block.
func validateSchedules(cfg *Config) []string {
	var errs []string
	for _, s := range cfg.Schedules {
		path := "schedules." + s.ID
		if !isIdentifier(s.ID) {
			errs = append(errs, fmt.Sprintf("%s: the id is carried into forge schedules and CI expressions; use letters, digits, - and _", path))
		}
		if strings.TrimSpace(s.Cron) == "" {
			errs = append(errs, path+".cron: required")
			continue
		}
		if _, err := ResolveCron(s.Cron, "", s.ID); err != nil {
			errs = append(errs, fmt.Sprintf("%s.cron: %v", path, err))
		}
		switch s.Build {
		case "", ScheduleBuildChanges, ScheduleBuildAlways:
		default:
			errs = append(errs, fmt.Sprintf("%s.build: unknown %q (changes | always)", path, s.Build))
		}
	}
	return errs
}
