package opentofu

import (
	"encoding/json"
	"fmt"
)

// Changes counts a plan's resource changes the way the binary's own summary does: a
// replacement counts as one add and one destroy.
type Changes struct {
	Add, Change, Destroy int
}

// None reports whether the plan changes nothing.
func (c Changes) None() bool { return c.Add == 0 && c.Change == 0 && c.Destroy == 0 }

func (c Changes) String() string {
	return fmt.Sprintf("%d to add, %d to change, %d to destroy", c.Add, c.Change, c.Destroy)
}

// ParsePlanChanges reads `show -json <planfile>` output.
func ParsePlanChanges(js []byte) (Changes, error) {
	var doc struct {
		ResourceChanges []struct {
			Change struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal(js, &doc); err != nil {
		return Changes{}, err
	}
	var c Changes
	for _, rc := range doc.ResourceChanges {
		for _, a := range rc.Change.Actions {
			switch a {
			case "create":
				c.Add++
			case "update":
				c.Change++
			case "delete":
				c.Destroy++
			}
		}
	}
	return c, nil
}
