package supplychain

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Cooldown is the supply-chain release cooldown (min_release_age): a release younger
// than its window is not recommended or adopted, which sidesteps the hours-to-days in
// which a compromised or broken release is live before it is yanked. The window can
// differ by update size, Dependabot-style:
//
//	min_release_age: 3d                                 # every update
//	min_release_age: {major: 14d, minor: 7d, patch: 3d} # per size
//	min_release_age: {default: 3d, major: 14d}          # unnamed sizes take default
//
// A dependency with a known vulnerability bypasses the cooldown: holding a fix exposes
// the repo to a known flaw to avoid a hypothetical one.
type Cooldown struct {
	Major, Minor, Patch time.Duration
}

// IsZero reports a disabled cooldown.
func (c Cooldown) IsZero() bool { return c.Major == 0 && c.Minor == 0 && c.Patch == 0 }

// Window is the cooldown for an update of the given size ("major", "minor", "patch").
// Any other size (a tag or codename change the version model cannot rank) takes the
// longest window.
func (c Cooldown) Window(updateType string) time.Duration {
	switch updateType {
	case "major":
		return c.Major
	case "minor":
		return c.Minor
	case "patch":
		return c.Patch
	}
	return c.Longest()
}

// Longest is the largest of the three windows.
func (c Cooldown) Longest() time.Duration {
	return max(c.Major, c.Minor, c.Patch)
}

// ParseCooldown reads the scalar or per-size form from a decoded value: a duration
// string, or a map of default/major/minor/patch to duration strings.
func ParseCooldown(v any) (Cooldown, error) {
	switch t := v.(type) {
	case nil:
		return Cooldown{}, nil
	case int, float64:
		return ParseCooldown(fmt.Sprint(t)) // a bare 0
	case string:
		d, err := ParseAge(t)
		if err != nil {
			return Cooldown{}, fmt.Errorf("min_release_age: %w", err)
		}
		return Cooldown{Major: d, Minor: d, Patch: d}, nil
	case map[string]any:
		raw := map[string]string{}
		for k, val := range t {
			switch val.(type) {
			case string, int, float64:
				raw[k] = fmt.Sprint(val)
			default:
				return Cooldown{}, fmt.Errorf("min_release_age.%s: must be a duration such as 3d, got %v", k, val)
			}
		}
		return cooldownFromMap(raw)
	}
	return Cooldown{}, fmt.Errorf("min_release_age: must be a duration (3d) or a map of major/minor/patch/default to durations")
}

func cooldownFromMap(raw map[string]string) (Cooldown, error) {
	parsed := map[string]time.Duration{}
	for k, s := range raw {
		switch k {
		case "default", "major", "minor", "patch":
		default:
			return Cooldown{}, fmt.Errorf("min_release_age.%s: unknown key; use default, major, minor or patch", k)
		}
		d, err := ParseAge(s)
		if err != nil {
			return Cooldown{}, fmt.Errorf("min_release_age.%s: %w", k, err)
		}
		parsed[k] = d
	}
	size := func(k string) time.Duration {
		if d, ok := parsed[k]; ok {
			return d
		}
		return parsed["default"]
	}
	return Cooldown{Major: size("major"), Minor: size("minor"), Patch: size("patch")}, nil
}

// UnmarshalYAML accepts the scalar or per-size form.
func (c *Cooldown) UnmarshalYAML(node *yaml.Node) error {
	var v any
	if err := node.Decode(&v); err != nil {
		return err
	}
	parsed, err := ParseCooldown(v)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// UnmarshalJSON accepts the scalar or per-size form (the freshness options round-trip).
func (c *Cooldown) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	parsed, err := ParseCooldown(v)
	if err != nil {
		return err
	}
	*c = parsed
	return nil
}

// MarshalJSON writes the per-size form, which UnmarshalJSON reads back.
func (c Cooldown) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"major": c.Major.String(), "minor": c.Minor.String(), "patch": c.Patch.String()})
}

// ParseAge reads a duration with day ("d") and week ("w") units added to Go's syntax:
// "3d", "2w", "72h". Empty or "0" is no cooldown.
func ParseAge(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "0" {
		return 0, nil
	}
	unit := time.Duration(0)
	switch {
	case strings.HasSuffix(s, "d"):
		unit = 24 * time.Hour
	case strings.HasSuffix(s, "w"):
		unit = 7 * 24 * time.Hour
	}
	if unit != 0 {
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%q is not a duration such as 3d, 2w or 72h", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%q is not a duration such as 3d, 2w or 72h", s)
	}
	return d, nil
}
